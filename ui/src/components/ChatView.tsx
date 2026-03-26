import { useRef, useEffect, useState, type KeyboardEvent } from 'react'
import { MessageBubble } from './MessageBubble'
import type { Message, ActiveToolCall } from '../types'

interface Props {
  messages: Message[]
  isStreaming: boolean
  streamingContent: string
  activeToolCalls: Map<number, ActiveToolCall>
  error: string | null
  onSend: (content: string) => void
  onApprove: (id: string) => void
  onDeny: (id: string) => void
  onAbort: () => void
}

export function ChatView({
  messages,
  isStreaming,
  streamingContent,
  activeToolCalls,
  error,
  onSend,
  onApprove,
  onDeny,
  onAbort,
}: Props) {
  const [input, setInput] = useState('')
  const bottomRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, streamingContent, activeToolCalls])

  useEffect(() => {
    if (!isStreaming) {
      textareaRef.current?.focus()
    }
  }, [isStreaming, messages.length])

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  const handleSend = () => {
    const trimmed = input.trim()
    if (!trimmed || isStreaming) return
    setInput('')
    onSend(trimmed)
  }

  // filter out system messages for display
  const visibleMessages = messages.filter(m => m.role !== 'system')

  return (
    <div className="chat-view">
      <div className="messages-container">
        <div className="messages">
          {visibleMessages.map((msg, i) => (
            <MessageBubble key={i} message={msg} />
          ))}

          {isStreaming && (
            <MessageBubble
              message={{ role: 'assistant', content: null }}
              isStreaming
              streamingContent={streamingContent}
              activeToolCalls={activeToolCalls}
              onApprove={onApprove}
              onDeny={onDeny}
            />
          )}

          {error && (
            <div className="error-message">
              {error}
              {!isStreaming && (
                <button
                  className="retry-btn"
                  onClick={() => {
                    const lastUser = [...messages].reverse().find(m => m.role === 'user')
                    if (lastUser?.content) onSend(lastUser.content)
                  }}
                >
                  Retry
                </button>
              )}
            </div>
          )}

          <div ref={bottomRef} />
        </div>
      </div>

      <div className="input-area">
        <textarea
          ref={textareaRef}
          className="chat-input"
          value={input}
          onChange={e => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="Send a message..."
          rows={1}
          disabled={isStreaming}
        />
        {isStreaming ? (
          <button className="send-btn" onClick={onAbort}>Stop</button>
        ) : (
          <button
            className="send-btn"
            onClick={handleSend}
            disabled={!input.trim()}
          >
            Send
          </button>
        )}
      </div>
    </div>
  )
}
