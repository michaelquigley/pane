/// <reference types="node" />
import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import type { Message, SSEEvent, TurnRecord } from '../types'
import {
  applyLifecycleEvent,
  assessRecovery,
  buildChatRequest,
  interruptTurn,
  LifecycleError,
  MAX_BODY_BYTES,
  NOT_EXECUTED_TEXT,
  prepareTurn,
  projectRecovery,
  reconcileTurn,
  rejectBeforeGeneration,
  utf8ByteLength,
  validateRecovery,
  type ChatRecord,
  type ChatRequestBody,
} from './turnRecord'

// the shared intake fixtures: every accepted or rejected request body below
// is produced by the browser's own serializer and replayed through the Go
// handler by internal/api/recoveryFixtures_test.go.
const fixtureDir = resolve(dirname(fileURLToPath(import.meta.url)), '../../../internal/api/testdata/recovery')
const update = process.env.PANE_UPDATE_FIXTURES === '1'

type Expect = 'accepted' | 'invalid_recovery' | 'recovery_required'

function fixture(name: string, expected: Expect, body: unknown) {
  const path = resolve(fixtureDir, `${name}.json`)
  const serialized = `${JSON.stringify({ expect: expected, body }, null, 2)}\n`
  if (update || !existsSync(path)) {
    mkdirSync(fixtureDir, { recursive: true })
    writeFileSync(path, serialized)
    return
  }
  expect(readFileSync(path, 'utf8')).toBe(serialized)
}

// a small driver for one turn's lifecycle events, numbering seq as the
// backend's sink does.
class Turn {
  seq = 0
  chat: ChatRecord
  readonly id: string
  constructor(chat: ChatRecord, id: string, content: string, alias = 'qwen') {
    this.id = id
    this.chat = prepareTurn(chat, content, id, alias)
  }
  event(event: Record<string, unknown>) {
    const result = applyLifecycleEvent(this.chat, this.id, { turn_id: this.id, seq: ++this.seq, ...event } as unknown as SSEEvent)
    if (result.kind === 'applied') this.chat = result.chat
    return this
  }
  start() {
    return this.event({ type: 'turn_start', alias: 'qwen' })
  }
  ready(round: number, content: string | null, calls: [string, string, string][]) {
    const round_id = `${this.id}-r${round}`
    const assistant: Message = { role: 'assistant', content, turn_id: this.id, round_id }
    if (calls.length) assistant.tool_calls = calls.map(([id, name, args]) => ({ id, type: 'function', function: { name, arguments: args } }))
    return this.event({ type: 'round_ready', round_id, assistant, finish: calls.length ? 'tool_calls' : 'stop' })
  }
  executing(round: number, id: string) {
    return this.event({ type: 'tool_call_executing', round_id: `${this.id}-r${round}`, index: 0, id, name: 'tool' })
  }
  result(round: number, id: string, execution: string, content: string, status = 'complete', error_code?: string) {
    return this.event({ type: 'tool_call_result', round_id: `${this.id}-r${round}`, index: 0, id, name: 'tool', status, content, duration_ms: 1, execution_state: execution, error_code })
  }
  complete(round: number) {
    const record = this.chat.turns.find(turn => turn.id === this.id)!
    const pending = record.rounds.find(r => r.round_id === `${this.id}-r${round}`)!
    const tool_messages = pending.calls.map(call => ({ role: 'tool', content: call.result?.content ?? 'tool call denied by user', tool_call_id: call.id, turn_id: this.id, round_id: pending.round_id }))
    return this.event({ type: 'round_complete', round_id: pending.round_id, assistant: pending.assistant, tool_messages })
  }
  end(outcome: string, execution: string) {
    return this.event({ type: 'turn_end', outcome, execution })
  }
}

const empty: ChatRecord = { messages: [], turns: [] }

function send(chat: ChatRecord, turnId: string, content: string): ChatRequestBody {
  const candidate = prepareTurn(chat, content, turnId, 'qwen')
  return buildChatRequest(candidate, turnId, 'qwen', 'default', '')
}

function completedTurn(): ChatRecord {
  return new Turn(empty, 't1', 'add 17 and 25')
    .start().ready(1, null, [['c1', 'add', '{"a":17,"b":25}']]).executing(1, 'c1').result(1, 'c1', 'result_received', '42')
    .complete(1).ready(2, 'the sum is 42', []).complete(2).end('completed', 'known').chat
}

describe('lifecycle events', () => {
  it('builds a completed record from the event stream', () => {
    const chat = completedTurn()
    const record = chat.turns[0]
    expect(record.state).toBe('completed')
    expect(record.rounds.every(round => round.committed && round.assistant === undefined)).toBe(true)
    expect(chat.messages.map(m => m.role)).toEqual(['user', 'assistant', 'tool', 'assistant'])
    expect(record.rounds[0].calls[0]).toMatchObject({ state: 'completed', result: { content: '42', is_error: false } })
  })

  it('ignores duplicates and rejects gaps, foreign turns, and malformed events', () => {
    const turn = new Turn(empty, 't1', 'hi').start()
    expect(applyLifecycleEvent(turn.chat, 't1', { type: 'turn_start', turn_id: 't1', seq: 1, alias: 'qwen' }).kind).toBe('duplicate')
    expect(() => applyLifecycleEvent(turn.chat, 't1', { type: 'turn_end', turn_id: 't1', seq: 3, outcome: 'completed', execution: 'none' })).toThrow(LifecycleError)
    expect(() => applyLifecycleEvent(turn.chat, 't1', { type: 'turn_end', turn_id: 'other', seq: 2, outcome: 'completed', execution: 'none' })).toThrow(LifecycleError)
    expect(() => applyLifecycleEvent(turn.chat, 't1', { type: 'round_ready', turn_id: 't1', seq: 2 } as unknown as SSEEvent)).toThrow(LifecycleError)
  })

  it('keeps continuation when no thinking was streamed', () => {
    const continuation = { format: 'codex-responses-items', v: 1, identity: { provider: 'openai-codex' }, items: [{ type: 'reasoning', id: 'rs_1', encrypted_content: 'enc', summary: [] }, { type: 'message', id: 'msg_1' }] }
    const turn = new Turn(empty, 't1', 'hi').start()
    turn.event({ type: 'round_ready', round_id: 't1-r1', finish: 'stop', assistant: { role: 'assistant', content: 'ok', turn_id: 't1', round_id: 't1-r1', continuation } })
    turn.event({ type: 'round_complete', round_id: 't1-r1', assistant: { role: 'assistant', content: 'ok', turn_id: 't1', round_id: 't1-r1', continuation }, tool_messages: [] })
    const saved = JSON.parse(JSON.stringify(turn.chat)) as ChatRecord
    expect(saved.messages[1].continuation).toEqual(continuation)
    expect(saved.messages[1].thinking).toBeUndefined()
    const body = send(saved, 't2', 'next')
    expect(body.messages[1].continuation).toEqual(continuation)
  })

  it('never sends saved display thinking, and keeps it out of the record', () => {
    const turn = new Turn(empty, 't1', 'hi').start()
    turn.event({ type: 'round_ready', round_id: 't1-r1', finish: 'stop', assistant: { role: 'assistant', content: 'ok', turn_id: 't1', round_id: 't1-r1' } })
    const result = applyLifecycleEvent(turn.chat, 't1', { type: 'round_complete', turn_id: 't1', seq: 3, round_id: 't1-r1', assistant: { role: 'assistant', content: 'ok', turn_id: 't1', round_id: 't1-r1' }, tool_messages: [] },
      assistant => ({ ...assistant, thinking: 'CANARY-THINKING' }))
    if (result.kind !== 'applied') throw new Error('not applied')
    const serialized = JSON.stringify(result.chat.messages)
    expect(serialized).toContain('CANARY-THINKING')
    expect(JSON.stringify(result.chat.turns)).not.toContain('CANARY-THINKING')
    expect(JSON.stringify(send(result.chat, 't2', 'next'))).not.toContain('CANARY-THINKING')
  })
})

describe('recovery assessment and projection', () => {
  it('classifies an abandoned marker as interrupted without rewriting it', () => {
    const chat = prepareTurn(empty, 'hi', 't1', 'qwen')
    expect(chat.turns[0].state).toBe('in_progress')
    const assessment = assessRecovery(chat.turns, null)
    expect(assessment.kind).toBe('reconcile')
    expect(chat.turns[0].state).toBe('in_progress')
    expect(assessRecovery(chat.turns, 't1').kind).toBe('clear')
  })

  it('offers retry only on affirmative non-execution', () => {
    const rejected = rejectBeforeGeneration(prepareTurn(empty, 'hi', 't1', 'qwen'), 't1', 'login_required')
    expect(assessRecovery(rejected.turns, null).kind).toBe('retryable')
    const lost = interruptTurn(new Turn(empty, 't1', 'hi').start().chat, 't1', 'partial')
    expect(assessRecovery(lost.turns, null).kind).toBe('reconcile')
  })

  it('projects idempotently across save and reload', () => {
    const turn = new Turn(empty, 't1', 'write').start().ready(1, null, [['c1', 'write_note', '{"text":"hello"}']]).executing(1, 'c1')
    const interrupted = interruptTurn(turn.chat, 't1', '')
    const reconciled = reconcileTurn(interrupted, 't1', { c1: { execution: 'unknown', note: 'i cannot confirm the outcome and choose to proceed without repeating this call.' } }, '', 'none')
    const reloaded = JSON.parse(JSON.stringify(reconciled)) as ChatRecord
    expect(projectRecovery(reloaded)).toBe(reloaded)
    expect(reloaded.messages.slice(1)).toEqual([
      { role: 'assistant', content: null, turn_id: 't1', round_id: 't1-r1', tool_calls: [{ id: 'c1', type: 'function', function: { name: 'write_note', arguments: '{"text":"hello"}' } }],
        tool_call_results: { c1: { status: 'complete', recovery: 'unknown', content: 'pane recovery: execution outcome unknown; no result was received.\noperator note: i cannot confirm the outcome and choose to proceed without repeating this call.' } } },
      { role: 'tool', turn_id: 't1', round_id: 't1-r1', tool_call_id: 'c1', recovery_placeholder: 'unknown', content: 'pane recovery: execution outcome unknown; no result was received.\noperator note: i cannot confirm the outcome and choose to proceed without repeating this call.' },
      { role: 'user', turn_id: 't1', content: 'operator reconciliation: c1: i cannot confirm the outcome and choose to proceed without repeating this call.' },
    ])
    const record = reloaded.turns[0]
    expect(record.rounds[0]).toMatchObject({ message_index: 1, committed: false })
    expect(record.rounds[0].calls[0].state).toBe('dispatched')
    expect(record.reconciliation).toMatchObject({ execution: 'unknown', message_index: 3 })
  })
})

describe('recovered display decorations', () => {
  it('derive from the same closures as the tool messages, survive reload, and are never sent', () => {
    const turn = new Turn(empty, 't1', 'go').start().ready(1, null, [['c1', 'read', '{}'], ['c2', 'read', '{}'], ['c3', 'read', '{}'], ['c4', 'write', '{}'], ['c5', 'write', '{}'], ['c6', 'write', '{}']])
      .executing(1, 'c1').result(1, 'c1', 'result_received', 'RECEIVED-42')
      .executing(1, 'c2').result(1, 'c2', 'result_received', 'permission denied', 'error', 'execution_error')
      .executing(1, 'c3').result(1, 'c3', 'result_received', '')
      .result(1, 'c4', 'not_dispatched', 'tool call denied by user', 'error', 'denied')
      .executing(1, 'c5').result(1, 'c5', 'unknown', 'error: lost', 'error', 'execution_error')
      .executing(1, 'c6')
    const reconciled = reconcileTurn(interruptTurn(turn.chat, 't1', ''), 't1', {
      c5: { execution: 'unknown', note: 'cannot tell.' },
      c6: { execution: 'known', note: 'the file changed.' },
    }, '', 'none')
    const reloaded = JSON.parse(JSON.stringify(reconciled)) as ChatRecord
    const assistant = reloaded.messages[1]
    const tools = reloaded.messages.slice(2, 8)
    expect(assistant.tool_call_results).toEqual({
      c1: { status: 'complete', content: 'RECEIVED-42' },
      c2: { status: 'error', content: 'permission denied' },
      c3: { status: 'complete', content: '' },
      c4: { status: 'complete', content: NOT_EXECUTED_TEXT, recovery: 'not_executed' },
      c5: { status: 'complete', content: tools[4].content, recovery: 'unknown' },
      c6: { status: 'complete', content: tools[5].content, recovery: 'operator_reported' },
    })
    for (const tool of tools) {
      expect(assistant.tool_call_results![tool.tool_call_id!].content).toBe(tool.content)
      expect(assistant.tool_call_results![tool.tool_call_id!].recovery).toBe(tool.recovery_placeholder)
    }
    // the retained evidence stays undecorated, and nothing decorative is sent.
    expect(reloaded.turns[0].rounds[0].assistant?.tool_call_results).toBeUndefined()
    const body = send(reloaded, 't2', 'next')
    expect(validateRecovery(body)).toBeNull()
    expect(JSON.stringify(body)).not.toContain('tool_call_results')
  })
})

describe('size limits', () => {
  it('measures serialized UTF-8 bytes, not string length', () => {
    // each 'é' is one UTF-16 unit but two UTF-8 bytes.
    const doc = { title: '', messages: [{ role: 'user', content: 'é'.repeat(1000) }] }
    const serialized = JSON.stringify(doc)
    expect(serialized.length).toBeLessThan(utf8ByteLength(serialized))
    expect(utf8ByteLength(serialized)).toBe(serialized.length + 1000)
  })

  it('accepts exactly the cap and rejects one byte over', () => {
    const prefix = '{"title":"","messages":[{"role":"user","content":"'
    const suffix = '"}],"createdAt":0,"updatedAt":0}'
    const fill = MAX_BODY_BYTES - utf8ByteLength(prefix + suffix)
    const atCap = prefix + 'é'.repeat(Math.floor(fill / 2)) + 'a'.repeat(fill % 2) + suffix
    expect(utf8ByteLength(atCap)).toBe(MAX_BODY_BYTES)
    expect(atCap.length).toBeLessThan(MAX_BODY_BYTES)
    expect(utf8ByteLength(atCap + ' ')).toBe(MAX_BODY_BYTES + 1)
  })
})

// ---------------------------------------------------------------------------
// intake fixtures, shared with the Go handler

function accepted(name: string, body: ChatRequestBody) {
  expect(validateRecovery(body)).toBeNull()
  fixture(name, 'accepted', body)
}

function rejected(name: string, code: Exclude<Expect, 'accepted'>, body: ChatRequestBody) {
  expect(validateRecovery(body)?.code).toBe(code)
  fixture(name, code, body)
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

describe('intake fixtures', () => {
  it('accepts a first turn with empty prior records', () => {
    accepted('empty-prior', send(empty, 't1', 'hello'))
  })

  it('accepts completed history with tool results', () => {
    accepted('completed-history', send(completedTurn(), 't2', 'double that'))
  })

  it('accepts legacy untagged history', () => {
    const legacy: ChatRecord = { messages: [
      { role: 'user', content: 'old question' },
      { role: 'assistant', content: null, tool_calls: [{ id: 'call-1', type: 'function', function: { name: 'read', arguments: '{}' } }] },
      { role: 'tool', content: 'old result', tool_call_id: 'call-1' },
      { role: 'assistant', content: 'old answer', thinking: 'display only' },
    ], turns: [] }
    accepted('legacy-history', send(legacy, 't1', 'new question'))
  })

  it('requires reconciliation for an unfinished marker with zero calls', () => {
    const abandoned = prepareTurn(empty, 'hi', 't1', 'qwen')
    rejected('unfinished-zero-calls', 'recovery_required', send(abandoned, 't2', 'next'))
  })

  it('requires reconciliation for pending, dispatched, and unknown calls', () => {
    const base = new Turn(empty, 't1', 'go').start().ready(1, null, [['c1', 'write', '{}']])
    rejected('pending-unreconciled', 'recovery_required', send(interruptTurn(base.chat, 't1', ''), 't2', 'next'))
    const dispatched = new Turn(empty, 't1', 'go').start().ready(1, null, [['c1', 'write', '{}']]).executing(1, 'c1')
    rejected('dispatched-unreconciled', 'recovery_required', send(interruptTurn(dispatched.chat, 't1', ''), 't2', 'next'))
    const unknown = new Turn(empty, 't1', 'go').start().ready(1, null, [['c1', 'write', '{}']]).executing(1, 'c1')
      .result(1, 'c1', 'unknown', 'error: connection lost', 'error', 'execution_error').end('failed', 'unknown')
    rejected('unknown-unreconciled', 'recovery_required', send(unknown.chat, 't2', 'next'))
  })

  it('retries on terminal non-execution without reconciliation', () => {
    const turn = new Turn(empty, 't1', 'go').start().ready(1, null, [['c1', 'write', '{}']])
      .event({ type: 'tool_call_approve', round_id: 't1-r1', index: 0, id: 'c1', name: 'write', arguments: '{}' })
      .end('cancelled', 'none')
    expect(assessRecovery(turn.chat.turns, null).kind).toBe('retryable')
    const body = send(turn.chat, 't2', 'go')
    expect(body.messages[2]).toMatchObject({ role: 'tool', recovery_placeholder: 'not_executed', content: NOT_EXECUTED_TEXT })
    accepted('terminal-non-execution', body)
  })

  it('continues from a known returned error through reconciliation', () => {
    const turn = new Turn(empty, 't1', 'go').start().ready(1, null, [['c1', 'read', '{}']]).executing(1, 'c1')
      .result(1, 'c1', 'result_received', 'permission denied', 'error', 'execution_error').end('failed', 'known')
    const reconciled = reconcileTurn(turn.chat, 't1', {}, 'the tool returned an error; continue from it.', 'none')
    expect(reconciled.turns[0].reconciliation?.execution).toBe('known')
    expect(reconciled.messages[2]).toEqual({ role: 'tool', content: 'permission denied', tool_call_id: 'c1', turn_id: 't1', round_id: 't1-r1' })
    accepted('known-returned-error', send(reconciled, 't2', 'what happened?'))
  })

  it('reproduces the three serialized examples', () => {
    const received = new Turn(empty, 't1', 'add').start().ready(1, null, [['c1', 'add', '{"a":17,"b":25}']]).executing(1, 'c1').result(1, 'c1', 'result_received', '42')
    const receivedBody = send(reconcileTurn(interruptTurn(received.chat, 't1', ''), 't1', {}, 'c1 returned 42; continue from that result without repeating it.', 'known'), 't2', 'double that result without calling a tool.')
    expect(receivedBody.messages.slice(1)).toEqual([
      { role: 'assistant', turn_id: 't1', round_id: 't1-r1', content: null, tool_calls: [{ id: 'c1', type: 'function', function: { name: 'add', arguments: '{"a":17,"b":25}' } }] },
      { role: 'tool', turn_id: 't1', round_id: 't1-r1', tool_call_id: 'c1', content: '42' },
      { role: 'user', turn_id: 't1', content: 'operator reconciliation: c1 returned 42; continue from that result without repeating it.' },
      { role: 'user', turn_id: 't2', content: 'double that result without calling a tool.' },
    ])
    accepted('example-received-result', receivedBody)

    const notRun = new Turn(empty, 't1', 'add').start().ready(1, null, [['c1', 'add', '{"a":17,"b":25}']])
      .result(1, 'c1', 'not_dispatched', 'mcp server unavailable', 'error', 'execution_error')
    const notRunBody = send(reconcileTurn(interruptTurn(notRun.chat, 't1', ''), 't1', {}, 'c1 was not dispatched; leave it unexecuted.', 'none'), 't2', 'answer without tools.')
    expect(notRunBody.messages[2]).toMatchObject({ role: 'tool', recovery_placeholder: 'not_executed', content: 'pane recovery: this call was not executed.' })
    accepted('example-known-non-execution', notRunBody)

    const uncertain = new Turn(empty, 't1', 'write').start().ready(1, null, [['c1', 'write_note', '{"text":"hello"}']]).executing(1, 'c1')
      .result(1, 'c1', 'unknown', 'error: connection lost', 'error', 'execution_error').end('failed', 'unknown')
    const uncertainBody = send(reconcileTurn(uncertain.chat, 't1', { c1: { execution: 'unknown', note: 'i cannot confirm the outcome and choose to proceed without repeating this call.' } }, '', 'none'), 't2', 'explain what remains uncertain; do not repeat the write.')
    accepted('example-acknowledged-unknown', uncertainBody)
  })

  it('covers operator-reported, not-executed, mixed, reconciliation-only, and missing-round cases', () => {
    const dispatched = new Turn(empty, 't1', 'go').start().ready(1, null, [['c1', 'write', '{}']]).executing(1, 'c1')
    accepted('operator-reported', send(reconcileTurn(interruptTurn(dispatched.chat, 't1', ''), 't1', { c1: { execution: 'known', note: 'the file exists with the new text.' } }, '', 'none'), 't2', 'next'))

    const pending = new Turn(empty, 't1', 'go').start().ready(1, null, [['c1', 'write', '{}']])
    accepted('pending-reconciled-not-executed', send(reconcileTurn(interruptTurn(pending.chat, 't1', ''), 't1', { c1: { execution: 'none', note: 'i denied it in another tab.' } }, '', 'none'), 't2', 'next'))

    const mixed = new Turn(empty, 't1', 'go').start().ready(1, null, [['c1', 'read', '{}'], ['c2', 'write', '{}']])
      .executing(1, 'c1').result(1, 'c1', 'result_received', '').executing(1, 'c2').result(1, 'c2', 'unknown', 'error: timeout', 'error', 'execution_error').end('failed', 'unknown')
    const mixedBody = send(reconcileTurn(mixed.chat, 't1', { c2: { execution: 'unknown', note: 'cannot tell.' } }, '', 'none'), 't2', 'next')
    expect(mixedBody.messages[2]).toMatchObject({ role: 'tool', content: '', tool_call_id: 'c1' })
    expect(mixedBody.messages[2].recovery_placeholder).toBeUndefined()
    accepted('mixed-received-and-unknown', mixedBody)

    const denied = new Turn(empty, 't1', 'go').start().ready(1, null, [['c1', 'write', '{}'], ['c2', 'read', '{"path":""}']])
      .result(1, 'c1', 'not_dispatched', 'tool call denied by user', 'error', 'denied')
      .result(1, 'c2', 'not_dispatched', 'error: malformed arguments', 'error', 'malformed_arguments')
    const deniedChat = interruptTurn(denied.chat, 't1', '')
    expect(deniedChat.turns[0].rounds[0].calls.map(call => call.state)).toEqual(['denied', 'rejected'])
    accepted('denied-and-rejected', send(reconcileTurn(deniedChat, 't1', {}, 'nothing ran.', 'none'), 't2', 'next'))

    const zero = interruptTurn(new Turn(empty, 't1', 'hi').start().chat, 't1', 'partial answer')
    accepted('reconciliation-only', send(reconcileTurn(zero, 't1', {}, 'no tools were offered.', 'none'), 't2', 'next'))
    const lost = interruptTurn(prepareTurn(empty, 'hi', 't1', 'qwen'), 't1', '')
    accepted('missing-finalized-round', send(reconcileTurn(lost, 't1', {}, 'the stream dropped before any round; outcome unknown.', 'unknown'), 't2', 'next'))
  })

  it('rejects hostile and malformed projections', () => {
    const uncertain = new Turn(empty, 't1', 'write').start().ready(1, null, [['c1', 'write_note', '{"text":"hello"}']]).executing(1, 'c1')
    const good = send(reconcileTurn(interruptTurn(uncertain.chat, 't1', ''), 't1', { c1: { execution: 'unknown', note: 'unsure.' } }, '', 'none'), 't2', 'next')
    const received = new Turn(empty, 't1', 'go').start().ready(1, null, [['c1', 'read', '{}'], ['c2', 'read', '{}']])
      .executing(1, 'c1').result(1, 'c1', 'result_received', 'one').executing(1, 'c2').result(1, 'c2', 'result_received', 'two').end('failed', 'known')
    const pair = send(reconcileTurn(received.chat, 't1', {}, 'continue.', 'none'), 't2', 'next')

    const masquerade = clone(good)
    masquerade.messages[2] = { role: 'tool', turn_id: 't1', round_id: 't1-r1', tool_call_id: 'c1', content: 'success' }
    rejected('placeholder-as-result', 'invalid_recovery', masquerade)

    const reordered = clone(pair)
    ;[reordered.messages[2], reordered.messages[3]] = [reordered.messages[3], reordered.messages[2]]
    rejected('reordered-results', 'invalid_recovery', reordered)

    const split = clone(pair)
    split.messages.splice(3, 0, split.messages[4])
    split.messages.splice(5, 1)
    split.recovery.turns[0].reconciliation!.message_index = 3
    rejected('note-splits-batch', 'invalid_recovery', split)

    const orphan = clone(pair)
    orphan.messages.splice(4, 0, { role: 'tool', turn_id: 't1', round_id: 't1-r1', tool_call_id: 'c2', content: 'two' })
    orphan.recovery.turns[0].reconciliation!.message_index = 5
    rejected('duplicate-result', 'invalid_recovery', orphan)

    const unbound = clone(good)
    unbound.recovery.turns = []
    rejected('missing-prior-record', 'invalid_recovery', unbound)

    const contradictory = clone(pair)
    delete contradictory.recovery.turns[0].rounds[0].calls[0].result
    rejected('contradictory-result', 'invalid_recovery', contradictory)

    const version = clone(good)
    ;(version.recovery as { v: number }).v = 2
    rejected('unsupported-version', 'invalid_recovery', version)

    const reused = clone(good)
    reused.recovery.turns[0].id = 't2'
    rejected('prior-record-reuses-fresh-id', 'invalid_recovery', reused)

    const unacknowledged = clone(good)
    delete unacknowledged.recovery.turns[0].reconciliation
    delete unacknowledged.recovery.turns[0].rounds[0].calls[0].reconciliation
    unacknowledged.messages.splice(3, 1)
    rejected('absent-operator-acknowledgement', 'invalid_recovery', unacknowledged)

    const inProgress = clone(good)
    ;(inProgress.recovery.turns[0] as TurnRecord).state = 'in_progress'
    rejected('prior-in-progress', 'recovery_required', inProgress)
  })

  it('rejects malformed fresh-turn shapes', () => {
    const base = send(completedTurn(), 't2', 'next')
    const cases: [string, (body: ChatRequestBody) => void][] = [
      ['fresh-empty-messages', body => { body.messages = [] }],
      ['fresh-mismatched-id', body => { body.turn_id = 't3' }],
      ['fresh-duplicated', body => { body.messages.splice(0, 0, { role: 'user', content: 'x', turn_id: 't2' }) }],
      ['fresh-assistant-role', body => { body.messages[body.messages.length - 1].role = 'assistant' }],
      ['fresh-not-final', body => { body.messages.push({ role: 'user', content: 'after', turn_id: 't1' }) }],
      ['fresh-with-metadata', body => { body.messages[body.messages.length - 1].round_id = 't2-r1' }],
      ['historical-id-without-record', body => { body.messages[0].turn_id = 'tx' }],
    ]
    for (const [name, mutate] of cases) {
      const body = clone(base)
      mutate(body)
      rejected(name, 'invalid_recovery', body)
    }
  })

  it('accepts several historical messages sharing a prior turn id', () => {
    const reconciled = reconcileTurn(interruptTurn(new Turn(empty, 't1', 'hi').start().chat, 't1', ''), 't1', {}, 'nothing ran.', 'none')
    const body = send(reconciled, 't2', 'next')
    expect(body.messages.filter(message => message.turn_id === 't1')).toHaveLength(2)
    accepted('shared-prior-turn-id', body)
  })
})
