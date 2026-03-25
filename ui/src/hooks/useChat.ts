import { useState, useRef, useCallback } from 'react'
import type { Message, ActiveToolCall, SSEEvent } from '../types'

export function useChat() {
  const [messages, setMessages] = useState<Message[]>([])
  const [isStreaming, setIsStreaming] = useState(false)
  const [streamingContent, setStreamingContent] = useState('')
  const [activeToolCalls, setActiveToolCalls] = useState<Map<string, ActiveToolCall>>(new Map())
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
    setMessages(allMessages)
    setIsStreaming(true)
    setStreamingContent('')
    setActiveToolCalls(new Map())
    setError(null)

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
      let contentAccum = ''
      const toolCallsAccum = new Map<string, ActiveToolCall>()
      // track finalized messages to append (assistant + tool results per round)
      const finalizedMessages: Message[] = []

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
              processEvent(
                event,
                contentAccum,
                (c) => { contentAccum = c },
                toolCallsAccum,
                setStreamingContent,
                setActiveToolCalls,
                setError,
                allMessages,
                finalizedMessages,
                setMessages,
              )
            } catch {
              // skip malformed JSON
            }
            currentEventType = ''
          }
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

function processEvent(
  event: SSEEvent,
  contentAccum: string,
  setContentAccum: (c: string) => void,
  toolCallsAccum: Map<string, ActiveToolCall>,
  setStreamingContent: (c: string | ((p: string) => string)) => void,
  setActiveToolCalls: (m: Map<string, ActiveToolCall> | ((p: Map<string, ActiveToolCall>) => Map<string, ActiveToolCall>)) => void,
  setError: (e: string | null) => void,
  baseMessages: Message[],
  finalizedMessages: Message[],
  setMessages: (m: Message[] | ((p: Message[]) => Message[])) => void,
) {
  switch (event.type) {
    case 'delta': {
      const newContent = contentAccum + event.content
      setContentAccum(newContent)
      setStreamingContent(newContent)
      break
    }

    case 'tool_call_start': {
      const tc: ActiveToolCall = {
        id: event.id,
        name: event.name,
        status: 'loading',
        argumentsSoFar: '',
      }
      toolCallsAccum.set(event.id, tc)
      setActiveToolCalls(new Map(toolCallsAccum))
      break
    }

    case 'tool_call_args': {
      const tc = toolCallsAccum.get(event.id)
      if (tc) {
        tc.argumentsSoFar += event.arguments_partial
        tc.status = 'args_streaming'
        setActiveToolCalls(new Map(toolCallsAccum))
      }
      break
    }

    case 'tool_call_approve': {
      const tc = toolCallsAccum.get(event.id)
      if (tc) {
        tc.status = 'awaiting_approval'
        tc.argumentsSoFar = event.arguments
        setActiveToolCalls(new Map(toolCallsAccum))
      }
      break
    }

    case 'tool_call_executing': {
      const tc = toolCallsAccum.get(event.id)
      if (tc) {
        tc.status = 'executing'
        setActiveToolCalls(new Map(toolCallsAccum))
      }
      break
    }

    case 'tool_call_result': {
      const tc = toolCallsAccum.get(event.id)
      if (tc) {
        tc.status = 'complete'
        tc.result = event.content
        tc.durationMs = event.duration_ms
        setActiveToolCalls(new Map(toolCallsAccum))
      }
      break
    }

    case 'error': {
      setError(event.message)
      break
    }

    case 'done': {
      // finalize: build assistant message with content + tool_calls, then tool result messages
      const assistantContent = contentAccum || null
      const toolCalls = Array.from(toolCallsAccum.values())

      const assistantMsg: Message = {
        role: 'assistant',
        content: assistantContent,
      }
      if (toolCalls.length > 0) {
        assistantMsg.tool_calls = toolCalls.map(tc => ({
          id: tc.id,
          type: 'function' as const,
          function: { name: tc.name, arguments: tc.argumentsSoFar },
        }))
      }
      finalizedMessages.push(assistantMsg)

      // add tool result messages
      for (const tc of toolCalls) {
        if (tc.result !== undefined) {
          finalizedMessages.push({
            role: 'tool',
            content: tc.result,
            tool_call_id: tc.id,
          })
        }
      }

      setMessages([...baseMessages, ...finalizedMessages])
      setStreamingContent('')
      setActiveToolCalls(new Map())

      // reset accumulators for next round (if tool loop continues)
      setContentAccum('')
      toolCallsAccum.clear()
      break
    }
  }
}
