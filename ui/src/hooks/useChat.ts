import { useState, useRef, useCallback } from 'react'
import type { Message, ActiveToolCall, SSEEvent } from '../types'

export function useChat() {
  const [messages, setMessages] = useState<Message[]>([])
  const [isStreaming, setIsStreaming] = useState(false)
  const [streamingContent, setStreamingContent] = useState('')
  const [activeToolCalls, setActiveToolCalls] = useState<Map<number, ActiveToolCall>>(new Map())
  const [error, setError] = useState<string | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  const sendMessage = useCallback(async (
    content: string,
    model: string,
    systemPrompt: string,
    disabledTools: string[],
  ) => {
    const userMessage: Message = { role: 'user', content }
    const allMessages = [...messages, userMessage]
    let committedMessages = allMessages
    let contentAccum = ''
    const toolCallsAccum = new Map<number, ActiveToolCall>()
    let receivedDone = false
    let sawErrorEvent = false

    setMessages(allMessages)
    setIsStreaming(true)
    setStreamingContent('')
    setActiveToolCalls(new Map())
    setError(null)

    // abort any in-flight request
    abortRef.current?.abort()

    const controller = new AbortController()
    abortRef.current = controller

    // build request messages: prepend system prompt if set
    const requestMessages: Message[] = []
    if (systemPrompt) {
      requestMessages.push({ role: 'system', content: systemPrompt })
    }
    // add conversation messages, skipping any existing system messages
    for (const msg of allMessages) {
      if (msg.role !== 'system') {
        requestMessages.push(msg)
      }
    }

    try {
      const response = await fetch('/api/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          model,
          messages: requestMessages,
          tools_disabled: disabledTools,
        }),
        signal: controller.signal,
      })

      if (!response.ok || !response.body) {
        setError(`HTTP ${response.status}`)
        setIsStreaming(false)
        return
      }

      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        let currentEventType = ''
        for (const line of lines) {
          if (line.startsWith('event: ')) {
            currentEventType = line.slice(7).trim()
          } else if (line.startsWith('data: ') && currentEventType) {
            const data = line.slice(6)
            try {
              const parsed = JSON.parse(data)
              const event = { type: currentEventType, ...parsed } as SSEEvent

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
                  tc.status = 'complete'
                  tc.result = event.content
                  tc.durationMs = event.duration_ms
                  setActiveToolCalls(new Map(toolCallsAccum))
                  break
                }

                case 'round_complete': {
                  committedMessages = [...committedMessages, event.assistant, ...event.tool_messages]
                  setMessages(committedMessages)
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
            currentEventType = ''
          }
        }
      }

      if (!receivedDone) {
        setStreamingContent('')
        setActiveToolCalls(new Map())
        if (!controller.signal.aborted && !sawErrorEvent) {
          setError('Connection lost')
        }
      }
    } catch (e) {
      if ((e as Error).name !== 'AbortError') {
        setError((e as Error).message)
      }
    } finally {
      setIsStreaming(false)
      abortRef.current = null
    }
  }, [messages])

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
    abortRef.current?.abort()
  }, [])

  return {
    messages,
    setMessages,
    isStreaming,
    streamingContent,
    activeToolCalls,
    error,
    sendMessage,
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
