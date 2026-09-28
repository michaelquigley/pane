import type {
  CallRecord,
  ToolCallResult,
  CallState,
  Execution,
  Message,
  RecoveryPlaceholder,
  RoundRecord,
  SSEEvent,
  SystemPromptMode,
  ToolCall,
  TurnRecord,
} from '../types'

// the portable recovery projection's generated text. the backend checks
// these exact strings against the recorded evidence.
export const NOT_EXECUTED_TEXT = 'pane recovery: this call was not executed.'
export const UNKNOWN_TEXT = 'pane recovery: execution outcome unknown; no result was received.'
export const OPERATOR_REPORTED_TEXT = 'pane recovery: no tool result was received; the outcome below is operator-reported.'
export const OPERATOR_SUFFIX = '\noperator note: '
export const NOTE_PREFIX = 'operator reconciliation: '

// the aggregate budget shared by one stored document and one chat body.
export const MAX_BODY_BYTES = 32 * 1024 * 1024

export function utf8ByteLength(serialized: string): number {
  return new TextEncoder().encode(serialized).byteLength
}

// the conversation's history paired with its turn records: the unit every
// record operation reads and returns.
export interface ChatRecord {
  messages: Message[]
  turns: TurnRecord[]
}

export function newTurnMarker(id: string, modelAlias: string, userMessageIndex: number): TurnRecord {
  return { id, v: 1, model_alias: modelAlias, user_message_index: userMessageIndex, state: 'in_progress', last_seq: 0, rounds: [] }
}

// an 'in_progress' record that no live request owns was abandoned: a reload,
// a closed tab, or a lost stream. it is interrupted, with its missing
// terminal evidence preserved.
export function effectiveState(record: TurnRecord, activeTurnId: string | null): TurnRecord['state'] {
  if (record.state === 'in_progress' && record.id !== activeTurnId) return 'interrupted'
  return record.state
}

export function isAmbiguous(state: CallState): boolean {
  return state === 'pending' || state === 'dispatched' || state === 'unknown'
}

function isKnownNonExecution(state: CallState): boolean {
  return state === 'denied' || state === 'rejected' || state === 'not_dispatched'
}

function isReceived(state: CallState): boolean {
  return state === 'completed' || state === 'failed'
}

// affirmative evidence that nothing in the turn ran: an observed turn_end
// with execution 'none' that no recorded call outcome contradicts.
export function terminalNonExecution(record: TurnRecord): boolean {
  if (record.terminal?.execution !== 'none') return false
  return record.rounds.every(round => round.calls.every(call => call.state === 'pending' || isKnownNonExecution(call.state)))
}

// ---------------------------------------------------------------------------
// lifecycle events

export class LifecycleError extends Error {}

type CriticalEvent = Extract<SSEEvent, { seq: number }>

const CRITICAL = new Set(['turn_start', 'round_ready', 'tool_call_approve', 'tool_call_executing', 'tool_call_result', 'round_complete', 'turn_end'])

export function isCriticalType(type: string): boolean {
  return CRITICAL.has(type)
}

// the outcome of applying one critical event: 'duplicate' is ignored, while
// a malformed, gapped, or foreign event throws LifecycleError so consumption
// stops conservatively rather than skipping recovery state.
export type LifecycleResult = { kind: 'applied'; chat: ChatRecord } | { kind: 'duplicate' }

function fail(message: string): never {
  throw new LifecycleError(message)
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function validToolCalls(value: unknown): value is ToolCall[] {
  return Array.isArray(value) && value.every(call => isObject(call) && typeof call.id === 'string' && call.id !== ''
    && isObject(call.function) && typeof call.function.name === 'string' && typeof call.function.arguments === 'string')
}

function replaceTurn(chat: ChatRecord, next: TurnRecord): ChatRecord {
  return { messages: chat.messages, turns: chat.turns.map(turn => turn.id === next.id ? next : turn) }
}

function findCall(record: TurnRecord, roundId: string | undefined, callId: string): { round: number; call: number } {
  const round = record.rounds.findIndex(r => r.round_id === roundId)
  if (round < 0) fail(`event names unknown round '${roundId}'`)
  const call = record.rounds[round].calls.findIndex(c => c.id === callId)
  if (call < 0) fail(`event names unknown call '${callId}'`)
  return { round, call }
}

function withCall(record: TurnRecord, at: { round: number; call: number }, update: (call: CallRecord) => CallRecord): TurnRecord {
  return {
    ...record,
    rounds: record.rounds.map((round, r) => r !== at.round ? round : {
      ...round,
      calls: round.calls.map((call, c) => c !== at.call ? call : update(call)),
    }),
  }
}

function callStateForResult(event: Extract<SSEEvent, { type: 'tool_call_result' }>): Pick<CallRecord, 'state' | 'result'> {
  switch (event.execution_state) {
    case 'result_received':
      return event.status === 'error'
        ? { state: 'failed', result: { content: event.content, is_error: true } }
        : { state: 'completed', result: { content: event.content, is_error: false } }
    case 'not_dispatched':
      if (event.error_code === 'denied') return { state: 'denied', result: undefined }
      if (event.error_code === 'malformed_arguments') return { state: 'rejected', result: undefined }
      return { state: 'not_dispatched', result: undefined }
    case 'unknown':
      return { state: 'unknown', result: undefined }
    default:
      return fail('tool result carries no execution evidence')
  }
}

// applyLifecycleEvent folds one critical event into the active turn's record.
// the record is the authoritative recovery state; visual deltas stay out of
// it. `decorate` lets the caller attach display-only fields (thinking,
// tool result decorations) to a promoted assistant.
export function applyLifecycleEvent(
  chat: ChatRecord,
  turnId: string,
  event: SSEEvent,
  decorate: (assistant: Message) => Message = assistant => assistant,
): LifecycleResult {
  if (!isCriticalType(event.type)) fail(`'${event.type}' is not a lifecycle event`)
  const critical = event as CriticalEvent
  if (critical.turn_id !== turnId) fail('event belongs to a different turn')
  const record = chat.turns.find(turn => turn.id === turnId)
  if (!record) fail('no record for the active turn')
  if (!Number.isInteger(critical.seq) || critical.seq < 1) fail('event has no valid sequence number')
  if (critical.seq <= record.last_seq) return { kind: 'duplicate' }
  if (critical.seq !== record.last_seq + 1) fail(`lifecycle sequence gap: expected ${record.last_seq + 1}, got ${critical.seq}`)
  const sequenced = { ...record, last_seq: critical.seq }

  switch (critical.type) {
    case 'turn_start': {
      if (record.last_seq !== 0) fail('turn_start is not the first lifecycle event')
      return { kind: 'applied', chat: replaceTurn(chat, { ...sequenced, origin: critical.origin }) }
    }
    case 'round_ready': {
      const assistant = critical.assistant
      if (typeof critical.round_id !== 'string' || !critical.round_id || !isObject(assistant) || assistant.role !== 'assistant'
        || assistant.turn_id !== turnId || assistant.round_id !== critical.round_id) fail('malformed round_ready')
      if (record.rounds.some(round => round.round_id === critical.round_id)) fail('round announced twice')
      const toolCalls = assistant.tool_calls ?? []
      if (!validToolCalls(toolCalls)) fail('round_ready carries malformed calls')
      const round: RoundRecord = {
        round_id: critical.round_id,
        assistant,
        calls: toolCalls.map(call => ({ id: call.id, type: 'function', function: { ...call.function }, state: 'pending' as const })),
        committed: false,
      }
      return { kind: 'applied', chat: replaceTurn(chat, { ...sequenced, rounds: [...record.rounds, round] }) }
    }
    case 'tool_call_approve': {
      findCall(record, critical.round_id, critical.id)
      return { kind: 'applied', chat: replaceTurn(chat, sequenced) }
    }
    case 'tool_call_executing': {
      const at = findCall(record, critical.round_id, critical.id)
      if (record.rounds[at.round].calls[at.call].state !== 'pending') fail('call dispatched twice')
      return { kind: 'applied', chat: replaceTurn(chat, withCall(sequenced, at, call => ({ ...call, state: 'dispatched' }))) }
    }
    case 'tool_call_result': {
      const at = findCall(record, critical.round_id, critical.id)
      const current = record.rounds[at.round].calls[at.call].state
      if (current !== 'pending' && current !== 'dispatched') fail('call outcome reported twice')
      if (typeof critical.content !== 'string') fail('malformed tool result')
      const outcome = callStateForResult(critical)
      return {
        kind: 'applied',
        chat: replaceTurn(chat, withCall(sequenced, at, call => {
          const next: CallRecord = { ...call, state: outcome.state }
          if (outcome.result) next.result = outcome.result
          else delete next.result
          return next
        })),
      }
    }
    case 'round_complete': {
      const r = record.rounds.findIndex(round => round.round_id === critical.round_id)
      if (r < 0) fail('round_complete names an unknown round')
      const round = record.rounds[r]
      const assistant = critical.assistant
      const tools = critical.tool_messages
      if (round.committed || !isObject(assistant) || assistant.round_id !== round.round_id || !Array.isArray(tools)
        || tools.length !== round.calls.length) fail('malformed round_complete')
      round.calls.forEach((call, i) => {
        const tool = tools[i]
        if (!isObject(tool) || tool.role !== 'tool' || tool.tool_call_id !== call.id || tool.round_id !== round.round_id
          || isAmbiguous(call.state)) fail('round_complete does not match its recorded calls')
      })
      const messageIndex = chat.messages.length
      const messages = [...chat.messages, decorate(assistant), ...tools]
      const promoted: RoundRecord = { round_id: round.round_id, message_index: messageIndex, calls: round.calls, committed: true }
      const next = { ...sequenced, rounds: record.rounds.map((existing, i) => i === r ? promoted : existing) }
      return { kind: 'applied', chat: { messages, turns: chat.turns.map(turn => turn.id === turnId ? next : turn) } }
    }
    case 'turn_end': {
      const { outcome, execution } = critical
      if (!['completed', 'failed', 'cancelled'].includes(outcome) || !['none', 'known', 'unknown'].includes(execution)) {
        fail('malformed turn_end')
      }
      const next: TurnRecord = { ...sequenced, terminal: { outcome, execution } }
      const complete = outcome === 'completed' && record.rounds.every(round => round.committed)
      next.state = complete ? 'completed' : 'interrupted'
      if (critical.error_code) next.error_code = critical.error_code
      if (critical.partial_text) next.partial_text = critical.partial_text
      return { kind: 'applied', chat: replaceTurn(chat, next) }
    }
  }
  return fail('unhandled lifecycle event')
}

// interruptTurn closes a turn whose stream ended without turn_end: an abort,
// a lost connection, or a rejected lifecycle event. the missing terminal
// evidence stays missing.
export function interruptTurn(chat: ChatRecord, turnId: string, partialText: string, errorCode?: string): ChatRecord {
  const record = chat.turns.find(turn => turn.id === turnId)
  if (!record || record.state !== 'in_progress') return chat
  const next: TurnRecord = { ...record, state: 'interrupted' }
  if (partialText) next.partial_text = partialText
  if (errorCode) next.error_code = errorCode
  return replaceTurn(chat, next)
}

// rejectBeforeGeneration records a typed pre-stream refusal. the backend
// rejects such requests before opening a stream or contacting a provider,
// so this is affirmative evidence that nothing ran.
export function rejectBeforeGeneration(chat: ChatRecord, turnId: string, errorCode: string): ChatRecord {
  const record = chat.turns.find(turn => turn.id === turnId)
  if (!record || record.state !== 'in_progress' || record.last_seq !== 0) return chat
  return replaceTurn(chat, { ...record, state: 'interrupted', error_code: errorCode, terminal: { outcome: 'failed', execution: 'none' } })
}

// ---------------------------------------------------------------------------
// recovery assessment and reconciliation

export type RecoveryAssessment =
  | { kind: 'clear' }
  | { kind: 'retryable'; record: TurnRecord }
  | { kind: 'reconcile'; record: TurnRecord; ambiguous: CallRecord[] }

// assessRecovery decides what the latest turn needs before another turn may
// start. retry is offered only on affirmative evidence of non-execution.
export function assessRecovery(turns: TurnRecord[] | undefined, activeTurnId: string | null): RecoveryAssessment {
  const record = turns?.[turns.length - 1]
  if (!record || record.id === activeTurnId) return { kind: 'clear' }
  const state = effectiveState(record, activeTurnId)
  if (state === 'completed' || record.reconciliation) return { kind: 'clear' }
  if (terminalNonExecution(record)) return { kind: 'retryable', record: { ...record, state } }
  const ambiguous = record.rounds.flatMap(round => round.calls.filter(call => isAmbiguous(call.state)))
  return { kind: 'reconcile', record: { ...record, state }, ambiguous }
}

export interface CallDecision {
  execution: Execution
  note: string
}

// reconcileTurn records the operator's structured account of an interrupted
// turn and places it in history. original states and received results stay
// as recorded; reconciliation annotates them. the turn's execution summary
// never understates the recorded evidence.
export function reconcileTurn(
  chat: ChatRecord,
  turnId: string,
  decisions: Record<string, CallDecision>,
  turnNote: string,
  turnExecution: Execution,
): ChatRecord {
  const record = chat.turns.find(turn => turn.id === turnId)
  if (!record) return chat
  let unknown = turnExecution === 'unknown'
  let known = turnExecution === 'known'
  const parts: string[] = []
  const rounds = record.rounds.map(round => ({
    ...round,
    calls: round.calls.map(call => {
      if (isReceived(call.state)) known = true
      const decision = decisions[call.id]
      if (!isAmbiguous(call.state) || !decision) return call
      const note = decision.note.trim()
      parts.push(`${call.id}: ${note}`)
      if (decision.execution === 'unknown') unknown = true
      if (decision.execution === 'known') known = true
      return { ...call, reconciliation: { execution: decision.execution, note } }
    }),
  }))
  if (turnNote.trim()) parts.push(turnNote.trim())
  const execution: Execution = unknown ? 'unknown' : known ? 'known' : 'none'
  const next: TurnRecord = {
    ...record,
    state: 'interrupted',
    rounds,
    reconciliation: { execution, note: NOTE_PREFIX + parts.join('; '), message_index: -1 },
  }
  return projectRecovery(replaceTurn(chat, next))
}

function placeholderFor(call: CallRecord, terminalNone: boolean): { content: string; kind?: RecoveryPlaceholder } | null {
  if (call.result && isReceived(call.state)) return { content: call.result.content }
  if (isKnownNonExecution(call.state)) return { content: NOT_EXECUTED_TEXT, kind: 'not_executed' }
  if (call.reconciliation) {
    const suffix = OPERATOR_SUFFIX + call.reconciliation.note
    if (call.reconciliation.execution === 'none') return { content: NOT_EXECUTED_TEXT + suffix, kind: 'not_executed' }
    if (call.reconciliation.execution === 'unknown') return { content: UNKNOWN_TEXT + suffix, kind: 'unknown' }
    return { content: OPERATOR_REPORTED_TEXT + suffix, kind: 'operator_reported' }
  }
  if (call.state === 'pending' && terminalNone) return { content: NOT_EXECUTED_TEXT, kind: 'not_executed' }
  return null
}

// projectRecovery appends each interrupted turn's finalized-but-unpromoted
// rounds to history -- the assistant, then exactly one tool-role message per
// call in call order -- followed by its operator reconciliation note. it is
// idempotent: rounds and notes already placed are never appended again, and
// a round whose calls cannot be truthfully closed is left unprojected. no
// executor is involved; placeholders are labeled provider scaffolding, never
// results.
export function projectRecovery(chat: ChatRecord): ChatRecord {
  let messages = chat.messages
  let changed = false
  const turns = chat.turns.map(record => {
    if (record.state === 'completed') return record
    const terminalNone = terminalNonExecution(record)
    let rounds = record.rounds
    for (let r = 0; r < rounds.length; r++) {
      const round = rounds[r]
      if (round.message_index !== undefined || !round.assistant) continue
      const closures = round.calls.map(call => placeholderFor(call, terminalNone))
      if (closures.some(closure => closure === null)) break
      const messageIndex = messages.length
      // the display decorations come from the same closures as the tool
      // messages: a received reply renders as its result, a placeholder as
      // an attributed recovery note. they are display-only and never sent.
      const results: Record<string, ToolCallResult> = {}
      round.calls.forEach((call, i) => {
        const closure = closures[i]!
        results[call.id] = closure.kind
          ? { status: 'complete', content: closure.content, recovery: closure.kind }
          : { status: call.state === 'failed' ? 'error' : 'complete', content: closure.content }
      })
      const assistant: Message = { ...round.assistant }
      if (round.calls.length > 0) assistant.tool_call_results = results
      const tools: Message[] = round.calls.map((call, i) => {
        const closure = closures[i]!
        const tool: Message = { role: 'tool', content: closure.content, tool_call_id: call.id, turn_id: record.id, round_id: round.round_id }
        if (closure.kind) tool.recovery_placeholder = closure.kind
        return tool
      })
      messages = [...messages, assistant, ...tools]
      rounds = rounds.map((existing, i) => i === r ? { ...existing, message_index: messageIndex } : existing)
      changed = true
    }
    let reconciliation = record.reconciliation
    if (reconciliation && reconciliation.message_index < 0 && rounds.every(round => round.message_index !== undefined)) {
      reconciliation = { ...reconciliation, message_index: messages.length }
      messages = [...messages, { role: 'user', content: reconciliation.note, turn_id: record.id }]
      changed = true
    }
    if (rounds === record.rounds && reconciliation === record.reconciliation) return record
    return { ...record, rounds, reconciliation }
  })
  return changed ? { messages, turns } : chat
}

// ---------------------------------------------------------------------------
// the chat request projection

export interface ChatRequestBody {
  model: string
  messages: Message[]
  system_prompt_mode: SystemPromptMode
  system_prompt?: string
  turn_id: string
  recovery: { v: 1; turns: TurnRecord[] }
}

function wireMessage(message: Message): Message {
  // display thinking, its collapse state, and tool result decorations never
  // reach the backend; recovery metadata and continuation do.
  const { thinking: _thinking, thinkingCollapsed: _collapsed, tool_call_results: _results, ...wire } = message
  return wire
}

function wireRecord(record: TurnRecord): TurnRecord {
  const { partial_text: _partial, ...wire } = record
  return wire
}

// buildChatRequest projects the acknowledged candidate into the /api/chat
// body: the full history, and every prior turn record except the fresh
// turn's own marker, which stays in the saved document only.
export function buildChatRequest(
  chat: ChatRecord,
  turnId: string,
  model: string,
  systemPromptMode: SystemPromptMode,
  systemPrompt: string,
): ChatRequestBody {
  const body: ChatRequestBody = {
    model,
    // indices in the records refer to this exact array; the backend drops
    // any system message itself, after validating against these positions.
    messages: chat.messages.map(wireMessage),
    system_prompt_mode: systemPromptMode,
    turn_id: turnId,
    recovery: { v: 1, turns: chat.turns.filter(record => record.id !== turnId).map(wireRecord) },
  }
  if (systemPromptMode === 'custom') body.system_prompt = systemPrompt
  return body
}

// prepareTurn builds the private pre-send candidate: the recovery projection
// of prior turns, the new user message, and its in-progress marker.
export function prepareTurn(chat: ChatRecord, content: string, turnId: string, modelAlias: string): ChatRecord {
  const projected = projectRecovery(chat)
  const messages = [...projected.messages, { role: 'user' as const, content, turn_id: turnId }]
  return { messages, turns: [...projected.turns, newTurnMarker(turnId, modelAlias, messages.length - 1)] }
}

// ---------------------------------------------------------------------------
// browser-side intake validation: the same rules the backend enforces, so a
// request that would be refused is caught before it is saved or sent.

export type RecoveryProblem = { code: 'invalid_recovery' | 'recovery_required'; message: string }

const ID = /^[A-Za-z0-9_.:-]{1,128}$/

function sameCalls(toolCalls: ToolCall[] | undefined, calls: CallRecord[]): boolean {
  const list = toolCalls ?? []
  return list.length === calls.length && calls.every((call, i) => list[i].id === call.id
    && list[i].function.name === call.function.name && list[i].function.arguments === call.function.arguments)
}

function canonical(value: unknown): string {
  return JSON.stringify(value, (_key, v) => isObject(v) ? Object.fromEntries(Object.entries(v).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0)) : v)
}

export function validateRecovery(body: ChatRequestBody): RecoveryProblem | null {
  const invalid = (message: string): RecoveryProblem => ({ code: 'invalid_recovery', message })
  const required = (message: string): RecoveryProblem => ({ code: 'recovery_required', message })
  const { messages, turn_id: fresh, recovery } = body
  if (!ID.test(fresh)) return invalid('invalid turn id')
  if (recovery.v !== 1) return invalid('unsupported recovery version')
  if (messages.length === 0) return invalid('no messages')
  const last = messages.length - 1
  const final = messages[last]
  if (final.turn_id !== fresh || final.role !== 'user' || final.round_id || final.tool_calls?.length || final.tool_call_id
    || final.origin || final.continuation || final.recovery_placeholder) return invalid('the final message must be the fresh user message')
  if (messages.slice(0, last).some(message => message.turn_id === fresh)) return invalid('fresh turn id reused')

  const claimed: (string | undefined)[] = new Array(messages.length)
  const roundIds = new Set<string>()
  const callIds = new Set<string>()
  const records = new Map<string, TurnRecord>()
  const claim = (index: number | undefined, owner: string): number | RecoveryProblem => {
    if (index === undefined || !Number.isInteger(index) || index < 0 || index >= last) return invalid(`turn '${owner}' has a dangling message index`)
    if (claimed[index]) return invalid(`message ${index} is bound twice`)
    claimed[index] = owner
    return index
  }

  for (const record of recovery.turns) {
    if (!ID.test(record.id) || record.id === fresh || records.has(record.id)) return invalid('invalid or duplicate turn record')
    records.set(record.id, record)
    if (record.v !== 1 || !['in_progress', 'completed', 'interrupted'].includes(record.state)) return invalid('malformed turn record')
    const userIndex = claim(record.user_message_index, record.id)
    if (typeof userIndex !== 'number') return userIndex
    const user = messages[userIndex]
    if (user.role !== 'user' || user.turn_id !== record.id || user.round_id || user.tool_calls?.length) return invalid('user message index mismatch')
    const terminalNone = terminalNonExecution(record)
    let position = userIndex
    for (const round of record.rounds) {
      if (!ID.test(round.round_id) || roundIds.has(round.round_id)) return invalid('invalid or duplicate round id')
      roundIds.add(round.round_id)
      for (const call of round.calls) {
        if (!ID.test(call.id) || callIds.has(call.id)) return invalid('invalid or duplicate call id')
        callIds.add(call.id)
        if (isReceived(call.state) !== !!call.result) return invalid(`call '${call.id}' result contradicts its state`)
        if (call.result && call.result.is_error !== (call.state === 'failed')) return invalid(`call '${call.id}' error flag contradicts its state`)
        if (call.reconciliation && (!isAmbiguous(call.state) || !call.reconciliation.note.trim())) return invalid(`call '${call.id}' has a malformed reconciliation`)
      }
      if (round.message_index === undefined) {
        if (round.committed || !round.assistant || !sameCalls(round.assistant.tool_calls, round.calls)) return invalid('round is neither represented nor retained')
        continue
      }
      const index = claim(round.message_index, record.id)
      if (typeof index !== 'number') return index
      if (index <= position) return invalid('round out of order')
      const assistant = messages[index]
      if (assistant.role !== 'assistant' || assistant.turn_id !== record.id || assistant.round_id !== round.round_id
        || assistant.recovery_placeholder || !sameCalls(assistant.tool_calls, round.calls)) return invalid('round does not match its assistant')
      if (round.committed ? !!round.assistant : !round.assistant
        || (round.assistant.content ?? '') !== (assistant.content ?? '')
        || canonical(round.assistant.origin ?? null) !== canonical(assistant.origin ?? null)
        || canonical(round.assistant.continuation ?? null) !== canonical(assistant.continuation ?? null)) return invalid('round evidence mismatch')
      for (let c = 0; c < round.calls.length; c++) {
        const call = round.calls[c]
        const toolIndex = claim(index + 1 + c, record.id)
        if (typeof toolIndex !== 'number') return toolIndex
        const tool = messages[toolIndex]
        if (tool.role !== 'tool' || tool.turn_id !== record.id || tool.round_id !== round.round_id || tool.tool_call_id !== call.id) {
          return invalid('tool results missing, extra, or out of order')
        }
        if (call.result) {
          if (tool.recovery_placeholder || (tool.content ?? '') !== call.result.content) return invalid('tool message does not carry its result')
        } else if (round.committed) {
          if (tool.recovery_placeholder || !isKnownNonExecution(call.state)) return invalid('committed round has an unresolved call')
        } else {
          const closure = placeholderFor(call, terminalNone)
          if (!closure || tool.recovery_placeholder !== closure.kind || tool.content !== closure.content) return invalid('recovery placeholder mismatch')
        }
      }
      position = index + round.calls.length
    }
    if (record.reconciliation) {
      const recon = record.reconciliation
      if (!recon.note.startsWith(NOTE_PREFIX) || recon.note.length === NOTE_PREFIX.length) return invalid('malformed reconciliation')
      const index = claim(recon.message_index, record.id)
      if (typeof index !== 'number') return index
      const note = messages[index]
      if (index <= position || note.role !== 'user' || note.turn_id !== record.id || note.round_id || note.content !== recon.note) {
        return invalid('reconciliation note mismatch')
      }
    }
  }

  for (let i = 0; i < last; i++) {
    const message = messages[i]
    if (!message.turn_id) {
      if (message.round_id || message.recovery_placeholder) return invalid('recovery metadata without a turn')
      continue
    }
    if (!records.has(message.turn_id)) return invalid(`message ${i} names a turn with no prior record`)
    if (claimed[i] !== message.turn_id) return invalid(`message ${i} is not bound by its turn record`)
  }

  for (const record of recovery.turns) {
    if (record.state === 'in_progress') return required('a prior turn is still in progress')
    if (record.state === 'completed') {
      if (record.terminal?.outcome !== 'completed' || record.reconciliation
        || record.rounds.some(round => !round.committed || round.calls.some(call => isAmbiguous(call.state) || call.reconciliation))) {
        return invalid('completed turn contradicts its evidence')
      }
      continue
    }
    const terminalNone = terminalNonExecution(record)
    let unknown = false
    let known = false
    for (const round of record.rounds) {
      if (round.message_index === undefined) return required('a finalized round is not yet in history')
      for (const call of round.calls) {
        if (isReceived(call.state)) known = true
        if (call.reconciliation) {
          if (!record.reconciliation?.note.includes(`${call.id}: ${call.reconciliation.note}`)) return invalid('call note missing from turn note')
          if (call.reconciliation.execution === 'unknown') unknown = true
          if (call.reconciliation.execution === 'known') known = true
          continue
        }
        if (isAmbiguous(call.state) && !(call.state === 'pending' && terminalNone)) return required(`call '${call.id}' is unresolved`)
      }
    }
    if (!record.reconciliation) {
      if (!terminalNone) return required('interrupted turn needs reconciliation')
      continue
    }
    const execution = record.reconciliation.execution
    if ((unknown && execution !== 'unknown') || (known && execution === 'none')) return invalid('reconciliation understates evidence')
  }
  return null
}
