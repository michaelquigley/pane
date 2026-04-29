import { useState, useRef, useCallback } from 'react'
import type { Message, ActiveToolCall, SSEEvent, ToolCallResult, SystemPromptMode } from '../types'
import { createSSEParser } from '../lib/sse'

interface SendMessageOptions {
  model: string
  systemPromptMode: SystemPromptMode
  systemPrompt: string
}

interface RequestSnapshot {
  requestMessages: Message[]
  options: SendMessageOptions
}

interface ActiveRequest {
  id: number
  controller: AbortController
}

export function useChat() {
  const [messages, setMessagesState] = useState<Message[]>([])
  const [isStreaming, setIsStreaming] = useState(false)
  const [streamingContent, setStreamingContent] = useState('')
  const [activeToolCalls, setActiveToolCalls] = useState<Map<number, ActiveToolCall>>(new Map())
  const [error, setError] = useState<string | null>(null)
  const activeRequestRef = useRef<ActiveRequest | null>(null)
  const nextRequestIdRef = useRef(0)
  const lastRequestRef = useRef<RequestSnapshot | null>(null)

  const executeRequest = useCallback(async (
    requestMessages: Message[],
    options: SendMessageOptions,
  ) => {
    lastRequestRef.current = {
      requestMessages,
      options: { ...options },
    }

    const previousRequest = activeRequestRef.current
    const controller = new AbortController()
    const request: ActiveRequest = {
      id: nextRequestIdRef.current + 1,
      controller,
    }
    nextRequestIdRef.current = request.id
    activeRequestRef.current = request
    previousRequest?.controller.abort()

    const isCurrentRequest = () => {
      const activeRequest = activeRequestRef.current
      return activeRequest?.id === request.id && activeRequest.controller === request.controller
    }

    let committedMessages = requestMessages
    let contentAccum = ''
    const toolCallsAccum = new Map<number, ActiveToolCall>()
    let receivedDone = false
    let sawErrorEvent = false

    setMessagesState(requestMessages)
    setIsStreaming(true)
    setStreamingContent('')
    setActiveToolCalls(new Map())
    setError(null)

    try {
      const response = await fetch('/api/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          model: options.model,
          messages: requestMessages,
          system_prompt_mode: options.systemPromptMode,
          ...(options.systemPromptMode === 'custom' ? { system_prompt: options.systemPrompt } : {}),
        }),
        signal: controller.signal,
      })

      if (!response.ok || !response.body) {
        if (isCurrentRequest()) {
          setError(`HTTP ${response.status}`)
        }
        return
      }

      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      const sseParser = createSSEParser()

      const handleParsedEvent = (eventType: string, data: string) => {
        if (!isCurrentRequest()) return

        try {
          const parsed = JSON.parse(data)
          const event = { type: eventType, ...parsed } as SSEEvent

          switch (event.type) {
            case 'delta': {
              contentAccum += event.content
              setStreamingContent(contentAccum)
              break
            }

            case 'tool_call_start': {
              const tc = getOrCreateActiveToolCall(toolCallsAccum, event.index)
              if (event.id) tc.id = event.id
              if (event.name) tc.name = event.name
              setActiveToolCalls(new Map(toolCallsAccum))
              break
            }

            case 'tool_call_args': {
              const tc = getOrCreateActiveToolCall(toolCallsAccum, event.index)
              if (event.id) tc.id = event.id
              tc.argumentsSoFar += event.arguments_partial
              tc.status = 'args_streaming'
              setActiveToolCalls(new Map(toolCallsAccum))
              break
            }

            case 'tool_call_approve': {
              const tc = getOrCreateActiveToolCall(toolCallsAccum, event.index)
              if (event.id) tc.id = event.id
              if (event.name) tc.name = event.name
              tc.status = 'awaiting_approval'
              tc.argumentsSoFar = event.arguments
              setActiveToolCalls(new Map(toolCallsAccum))
              break
            }

            case 'tool_call_executing': {
              const tc = getOrCreateActiveToolCall(toolCallsAccum, event.index)
              if (event.id) tc.id = event.id
              if (event.name) tc.name = event.name
              tc.status = 'executing'
              setActiveToolCalls(new Map(toolCallsAccum))
              break
            }

            case 'tool_call_result': {
              const tc = getOrCreateActiveToolCall(toolCallsAccum, event.index)
              if (event.id) tc.id = event.id
              if (event.name) tc.name = event.name
              tc.status = event.status
              tc.result = event.content
              tc.durationMs = event.duration_ms
              tc.errorCode = event.error_code
              setActiveToolCalls(new Map(toolCallsAccum))
              break
            }

            case 'round_complete': {
              const assistantMessage = attachToolCallResults(event.assistant, toolCallsAccum)
              committedMessages = [...committedMessages, assistantMessage, ...event.tool_messages]
              setMessagesState(committedMessages)
              contentAccum = ''
              toolCallsAccum.clear()
              setStreamingContent('')
              setActiveToolCalls(new Map())
              break
            }

            case 'error': {
              sawErrorEvent = true
              setError(event.message)
              break
            }

            case 'done': {
              receivedDone = true
              setStreamingContent('')
              setActiveToolCalls(new Map())
              break
            }
          }
        } catch {
          // skip malformed JSON
        }
      }

      while (true) {
        if (!isCurrentRequest()) break

        const { done, value } = await reader.read()
        if (done) break
        if (!isCurrentRequest()) break

        const chunk = decoder.decode(value, { stream: true })
        for (const event of sseParser.push(chunk)) {
          if (!isCurrentRequest()) break
          handleParsedEvent(event.type, event.data)
        }
      }

      const finalChunk = decoder.decode()
      if (finalChunk && isCurrentRequest()) {
        for (const event of sseParser.push(finalChunk)) {
          if (!isCurrentRequest()) break
          handleParsedEvent(event.type, event.data)
        }
      }

      if (!receivedDone && isCurrentRequest()) {
        setStreamingContent('')
        setActiveToolCalls(new Map())
        if (!controller.signal.aborted && !sawErrorEvent) {
          setError('Connection lost')
        }
      }
    } catch (e) {
      if (isCurrentRequest() && (e as Error).name !== 'AbortError') {
        setError((e as Error).message)
      }
    } finally {
      if (isCurrentRequest()) {
        setIsStreaming(false)
        activeRequestRef.current = null
      }
    }
  }, [])

  const sendMessage = useCallback(async (
    content: string,
    options: SendMessageOptions,
  ) => {
    const userMessage: Message = { role: 'user', content }
    const requestMessages = [...messages, userMessage].filter(msg => msg.role !== 'system')
    await executeRequest(requestMessages, options)
  }, [executeRequest, messages])

  const retryLastRequest = useCallback(async () => {
    if (!lastRequestRef.current || isStreaming) return
    const { requestMessages, options } = lastRequestRef.current
    await executeRequest(requestMessages, options)
  }, [executeRequest, isStreaming])

  const setMessages = useCallback((value: Message[] | ((prev: Message[]) => Message[])) => {
    const activeRequest = activeRequestRef.current
    activeRequestRef.current = null
    activeRequest?.controller.abort()

    lastRequestRef.current = null
    setIsStreaming(false)
    setError(null)
    setStreamingContent('')
    setActiveToolCalls(new Map())
    setMessagesState(value)
  }, [])

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

  const abort = useCallback(() => {
    const activeRequest = activeRequestRef.current
    activeRequestRef.current = null
    activeRequest?.controller.abort()

    setIsStreaming(false)
    setStreamingContent('')
    setActiveToolCalls(new Map())
  }, [])

  return {
    messages,
    setMessages,
    isStreaming,
    streamingContent,
    activeToolCalls,
    error,
    sendMessage,
    retryLastRequest,
    approveToolCall,
    denyToolCall,
    abort,
  }
}

function getOrCreateActiveToolCall(
  toolCallsAccum: Map<number, ActiveToolCall>,
  index: number,
): ActiveToolCall {
  let tc = toolCallsAccum.get(index)
  if (!tc) {
    tc = {
      index,
      name: '',
      status: 'loading',
      argumentsSoFar: '',
    }
    toolCallsAccum.set(index, tc)
  }
  return tc
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
