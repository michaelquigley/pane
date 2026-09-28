import { useState, useRef, useCallback } from 'react'
import type { Message, ActiveToolCall, Conversation, SSEEvent, ToolCallResult, UsageRecord, TurnRecord, Execution } from '../types'
import { createSSEParser } from '../lib/sse'
import { usageRecordFromEvent } from '../lib/usageRecord'
import { applyToolPreview, getOrCreateActiveToolCall } from '../lib/toolPreview'
import {
  applyLifecycleEvent,
  interruptTurn,
  isCriticalType,
  LifecycleError,
  reconcileTurn,
  rejectBeforeGeneration,
  type CallDecision,
  type ChatRecord,
  type ChatRequestBody,
} from '../lib/turnRecord'

interface ActiveRequest {
  id: number
  turnId: string
  controller: AbortController
  // the current round's streamed answer text: display-only partial evidence
  // for an interruption, reset once a round is finalized.
  partial: string
}

// a typed failure the backend returns before any stream opens.
export interface ChatFailure {
  code: string
  message: string
}

async function decodeFailure(response: Response): Promise<ChatFailure | null> {
  try {
    const body = await response.json()
    if (typeof body?.error?.code === 'string' && typeof body?.error?.message === 'string') {
      return { code: body.error.code, message: body.error.message }
    }
  } catch {
    // an untyped body carries no evidence about what ran
  }
  return null
}

export function useChat() {
  const [messages, setMessagesState] = useState<Message[]>([])
  const [turns, setTurnsState] = useState<TurnRecord[]>([])
  const [isStreaming, setIsStreaming] = useState(false)
  const [activeTurnId, setActiveTurnId] = useState<string | null>(null)
  const [streamingContent, setStreamingContent] = useState('')
  const [streamingThinking, setStreamingThinking] = useState('')
  const [activeToolCalls, setActiveToolCalls] = useState<Map<number, ActiveToolCall>>(new Map())
  const [error, setError] = useState<string | null>(null)
  const [usageRecord, setUsageRecord] = useState<UsageRecord | null>(null)
  const activeRequestRef = useRef<ActiveRequest | null>(null)
  const nextRequestIdRef = useRef(0)
  // the working record: history and turn records move together, and every
  // update goes through commit so a late writer never clobbers the other.
  const recordRef = useRef<ChatRecord>({ messages: [], turns: [] })

  const commit = useCallback((next: ChatRecord) => {
    const previous = recordRef.current
    recordRef.current = next
    if (next.messages !== previous.messages) setMessagesState(next.messages)
    if (next.turns !== previous.turns) setTurnsState(next.turns)
  }, [])

  const clearTransient = useCallback(() => {
    setStreamingContent('')
    setStreamingThinking('')
    setActiveToolCalls(new Map())
  }, [])

  // startTurn installs the acknowledged candidate and starts its POST. the
  // fetch is issued synchronously, before this function's first await, so
  // the caller can release its preparation guard knowing the request began.
  const startTurn = useCallback((candidate: ChatRecord, body: ChatRequestBody, usageModel: string) => {
    const turnId = body.turn_id
    const previousRequest = activeRequestRef.current
    const controller = new AbortController()
    const request: ActiveRequest = { id: nextRequestIdRef.current + 1, turnId, controller, partial: '' }
    nextRequestIdRef.current = request.id
    activeRequestRef.current = request
    previousRequest?.controller.abort()

    const isCurrentRequest = () => activeRequestRef.current?.id === request.id

    commit(candidate)
    setIsStreaming(true)
    setActiveTurnId(turnId)
    clearTransient()
    setError(null)
    setUsageRecord(null)

    const responsePromise = fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      signal: controller.signal,
    })

    let thinkingAccum = ''
    const toolCallsAccum = new Map<number, ActiveToolCall>()
    let sawTurnEnd = false
    let sawErrorEvent = false
    let lifecycleLost: string | null = null

    const interrupt = (code?: string) => {
      commit(interruptTurn(recordRef.current, turnId, request.partial, code))
    }

    const handleParsedEvent = (eventType: string, data: string) => {
      if (!isCurrentRequest() || lifecycleLost) return
      let event: SSEEvent
      try {
        event = { type: eventType, ...JSON.parse(data) } as SSEEvent
      } catch {
        // a malformed critical event is loss of recovery information, not
        // something to skip; a malformed display event is only display.
        if (isCriticalType(eventType)) lifecycleLost = `malformed '${eventType}' event`
        return
      }

      if (isCriticalType(event.type)) {
        try {
          const result = applyLifecycleEvent(recordRef.current, turnId, event, assistant => {
            const decorated = attachToolCallResults(assistant, toolCallsAccum)
            return thinkingAccum ? { ...decorated, thinking: thinkingAccum } : decorated
          })
          if (result.kind === 'duplicate') return
          commit(result.chat)
        } catch (reason) {
          if (reason instanceof LifecycleError) {
            lifecycleLost = reason.message
            return
          }
          throw reason
        }
      } else if ('turn_id' in event && event.turn_id !== undefined && event.turn_id !== turnId) {
        return
      }

      switch (event.type) {
        case 'delta':
          request.partial += event.content
          setStreamingContent(request.partial)
          break
        case 'thinking_delta':
          thinkingAccum += event.content
          setStreamingThinking(thinkingAccum)
          break
        case 'tool_call_start':
        case 'tool_call_args':
          setActiveToolCalls(applyToolPreview(toolCallsAccum, event))
          break
        case 'round_ready':
          request.partial = ''
          break
        case 'tool_call_approve': {
          const tc = getOrCreateActiveToolCall(toolCallsAccum, event.index)
          tc.id = event.id
          tc.name = event.name
          tc.status = 'awaiting_approval'
          tc.argumentsSoFar = event.arguments
          setActiveToolCalls(new Map(toolCallsAccum))
          break
        }
        case 'tool_call_executing': {
          const tc = getOrCreateActiveToolCall(toolCallsAccum, event.index)
          tc.id = event.id
          tc.name = event.name
          tc.status = 'executing'
          setActiveToolCalls(new Map(toolCallsAccum))
          break
        }
        case 'tool_call_result': {
          const tc = getOrCreateActiveToolCall(toolCallsAccum, event.index)
          tc.id = event.id
          tc.name = event.name
          tc.status = event.status
          tc.result = event.content
          tc.durationMs = event.duration_ms
          tc.errorCode = event.error_code
          setActiveToolCalls(new Map(toolCallsAccum))
          break
        }
        case 'usage':
          setUsageRecord(usageRecordFromEvent(event, usageModel))
          break
        case 'round_complete':
          request.partial = ''
          thinkingAccum = ''
          toolCallsAccum.clear()
          clearTransient()
          break
        case 'turn_end':
          sawTurnEnd = true
          clearTransient()
          break
        case 'error':
          sawErrorEvent = true
          setError(event.message)
          break
        case 'done':
          clearTransient()
          break
      }
    }

    void (async () => {
      try {
        const response = await responsePromise
        if (!response.ok || !response.body) {
          if (!isCurrentRequest()) return
          const failure = await decodeFailure(response)
          if (failure) {
            // a typed refusal happens before any stream or provider request.
            commit(rejectBeforeGeneration(recordRef.current, turnId, failure.code))
            setError(failure.message)
          } else {
            interrupt()
            setError(`HTTP ${response.status}`)
          }
          return
        }

        const reader = response.body.getReader()
        const decoder = new TextDecoder()
        const sseParser = createSSEParser()
        const consume = (chunk: string) => {
          for (const event of sseParser.push(chunk)) {
            if (!isCurrentRequest() || lifecycleLost) break
            handleParsedEvent(event.type, event.data)
          }
        }

        while (isCurrentRequest() && !lifecycleLost) {
          const { done, value } = await reader.read()
          if (done || !isCurrentRequest()) break
          consume(decoder.decode(value, { stream: true }))
        }
        if (isCurrentRequest() && !lifecycleLost) consume(decoder.decode())

        if (lifecycleLost && isCurrentRequest()) {
          controller.abort()
          interrupt('lifecycle_lost')
          clearTransient()
          setError(`recovery information was lost (${lifecycleLost}); reconcile before continuing`)
        } else if (!sawTurnEnd && isCurrentRequest()) {
          interrupt()
          clearTransient()
          if (!controller.signal.aborted && !sawErrorEvent) setError('connection lost')
        }
      } catch (e) {
        if (isCurrentRequest()) {
          interrupt()
          clearTransient()
          if ((e as Error).name !== 'AbortError') setError((e as Error).message)
        }
      } finally {
        if (isCurrentRequest()) {
          setIsStreaming(false)
          setActiveTurnId(null)
          activeRequestRef.current = null
        }
      }
    })()
  }, [clearTransient, commit])

  // stop the active request. its record becomes interrupted: the backend may
  // have dispatched a tool whose outcome this tab never received.
  // returns the interrupted record when a turn was active, so a caller that
  // is about to navigate away can persist it to the turn's own conversation.
  const abort = useCallback((): ChatRecord | null => {
    const activeRequest = activeRequestRef.current
    activeRequestRef.current = null
    activeRequest?.controller.abort()
    let interrupted: ChatRecord | null = null
    if (activeRequest) {
      interrupted = interruptTurn(recordRef.current, activeRequest.turnId, activeRequest.partial)
      commit(interrupted)
    }

    setIsStreaming(false)
    setActiveTurnId(null)
    clearTransient()
    return interrupted
  }, [clearTransient, commit])

  const loadConversation = useCallback((conversation: Conversation | null) => {
    const activeRequest = activeRequestRef.current
    activeRequestRef.current = null
    activeRequest?.controller.abort()
    setIsStreaming(false)
    setActiveTurnId(null)
    clearTransient()
    setError(null)
    // a load is a read: the stored references go into state unchanged, and
    // an abandoned marker is classified at use, not rewritten here.
    recordRef.current = { messages: conversation?.messages ?? [], turns: conversation?.turns ?? [] }
    setMessagesState(recordRef.current.messages)
    setTurnsState(recordRef.current.turns)
    setUsageRecord(conversation?.usage ?? null)
  }, [clearTransient])

  const reconcile = useCallback((turnId: string, decisions: Record<string, CallDecision>, note: string, execution: Execution) => {
    if (activeRequestRef.current) return
    commit(reconcileTurn(recordRef.current, turnId, decisions, note, execution))
  }, [commit])

  const setThinkingCollapsed = useCallback((messageIndex: number, collapsed: boolean) => {
    const current = recordRef.current
    commit({
      ...current,
      messages: current.messages.map((message, i) =>
        i === messageIndex ? { ...message, thinkingCollapsed: collapsed ? true : undefined } : message),
    })
  }, [commit])

  const approveToolCall = useCallback((id: string) => {
    fetch('/api/tools/approve', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id, approved: true }),
    })
  }, [])

  const denyToolCall = useCallback((id: string) => {
    fetch('/api/tools/approve', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id, approved: false }),
    })
  }, [])

  const clearError = useCallback(() => setError(null), [])
  const hasActiveTurn = useCallback(() => activeRequestRef.current !== null, [])

  return {
    messages,
    turns,
    recordRef,
    loadConversation,
    usageRecord,
    isStreaming,
    activeTurnId,
    hasActiveTurn,
    streamingContent,
    streamingThinking,
    setThinkingCollapsed,
    activeToolCalls,
    error,
    setError,
    clearError,
    startTurn,
    reconcile,
    approveToolCall,
    denyToolCall,
    abort,
  }
}

function attachToolCallResults(
  assistant: Message,
  toolCallsAccum: Map<number, ActiveToolCall>,
): Message {
  const toolCallResults: Record<string, ToolCallResult> = {}

  for (const toolCall of toolCallsAccum.values()) {
    if (!toolCall.id || toolCall.result === undefined) continue
    toolCallResults[toolCall.id] = {
      status: toolCall.status === 'error' ? 'error' : 'complete',
      error_code: toolCall.errorCode,
      content: toolCall.result,
      duration_ms: toolCall.durationMs ?? 0,
    }
  }

  if (Object.keys(toolCallResults).length === 0) {
    return assistant
  }

  return {
    ...assistant,
    tool_call_results: toolCallResults,
  }
}
