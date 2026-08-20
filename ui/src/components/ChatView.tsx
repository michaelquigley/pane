import { useRef, useEffect, useState, type KeyboardEvent } from 'react'
import { MessageBubble } from './MessageBubble'
import type { Message, ActiveToolCall } from '../types'

interface Props {
  messages: Message[]
  isStreaming: boolean
  streamingContent: string
  streamingThinking: string
  activeToolCalls: Map<number, ActiveToolCall>
  error: string | null
  appError: string | null
  canSend: boolean
  onSend: (content: string) => void
  onRetry: () => void
  onApprove: (id: string) => void
  onDeny: (id: string) => void
  onAbort: () => void
  onToggleThinkingCollapsed: (messageIndex: number, collapsed: boolean) => void
}

export function ChatView({
  messages,
  isStreaming,
  streamingContent,
  streamingThinking,
  activeToolCalls,
  error,
  appError,
  canSend,
  onSend,
  onRetry,
  onApprove,
  onDeny,
  onAbort,
  onToggleThinkingCollapsed,
}: Props) {
  const [input, setInput] = useState('')
  const bottomRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    resizeTextarea(textareaRef.current)
  }, [input])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, streamingContent, streamingThinking, activeToolCalls])

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
    // the gate sits before the input clear, so a send refused by the session
    // gate leaves the typed text where the reader left it.
    if (!input.trim() || isStreaming || !canSend) return
    const content = input
    setInput('')
    onSend(content)
  }

  // visible messages keep their index in the full messages array, so the
  // thinking-collapse toggle addresses the exact message a round committed
  const visibleMessages = messages
    .map((msg, index) => ({ msg, index }))
    .filter(({ msg }) => msg.role !== 'system' && msg.role !== 'tool')

  return (
    <div className="chat-view">
      <div className="messages-container">
        <div className="messages">
          {visibleMessages.map(({ msg, index }, i) => (
            <MessageBubble
              key={index}
              message={msg}
              compact={shouldCompactMessage(msg, visibleMessages[i - 1]?.msg)}
              onToggleThinking={collapsed => onToggleThinkingCollapsed(index, collapsed)}
            />
          ))}

          {isStreaming && (
            <MessageBubble
              message={{ role: 'assistant', content: null }}
              isStreaming
              streamingContent={streamingContent}
              streamingThinking={streamingThinking}
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
                  onClick={() => { if (canSend) onRetry() }}
                  disabled={!canSend}
                >
                  Retry
                </button>
              )}
            </div>
          )}

          <div ref={bottomRef} />
        </div>
      </div>

      {appError && <div className="app-notice">{appError}</div>}

      <div className="input-area">
        <textarea
          ref={textareaRef}
          className="chat-input"
          value={input}
          onChange={e => {
            setInput(e.target.value)
            resizeTextarea(e.target)
          }}
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
            disabled={!input.trim() || !canSend}
          >
            Send
          </button>
        )}
      </div>
    </div>
  )
}

function shouldCompactMessage(message: Message, previous?: Message): boolean {
  return isToolOnlyAssistantMessage(message) && (
    !previous || isToolOnlyAssistantMessage(previous) || previous.role === 'assistant'
  )
}

function isToolOnlyAssistantMessage(message: Message): boolean {
  return message.role === 'assistant'
    && !message.content
    && !!message.tool_calls?.length
}

function resizeTextarea(textarea: HTMLTextAreaElement | null) {
  if (!textarea) return
  textarea.style.height = 'auto'
  textarea.style.height = `${textarea.scrollHeight}px`
}
