import { useRef, useEffect, useState, type KeyboardEvent, type ReactNode } from 'react'
import { MessageBubble } from './MessageBubble'
import type { Message, ActiveToolCall } from '../types'

// one shared object, so the memoized streaming bubble compares equal when
// the view re-renders for an unrelated reason
const streamingPlaceholder: Message = { role: 'assistant', content: null }

interface Props {
  messages: Message[]
  isStreaming: boolean
  streamingContent: string
  streamingThinking: string
  activeToolCalls: Map<number, ActiveToolCall>
  error: string | null
  appError: string | null
  canSend: boolean
  // why sending is unavailable, shown beside the composer: a signed-out
  // model, or an interrupted turn awaiting reconciliation.
  sendNotice?: string | null
  // resolves true once the chat request has started; the draft is kept on
  // any other outcome.
  onSend: (content: string) => Promise<boolean>
  preparing: boolean
  recovery?: ReactNode
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
  sendNotice,
  onSend,
  preparing,
  recovery,
  onApprove,
  onDeny,
  onAbort,
  onToggleThinkingCollapsed,
}: Props) {
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, streamingContent, streamingThinking, activeToolCalls])

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
              messageIndex={index}
              compact={shouldCompactMessage(msg, visibleMessages[i - 1]?.msg)}
              onToggleThinking={onToggleThinkingCollapsed}
            />
          ))}

          {isStreaming && (
            <MessageBubble
              message={streamingPlaceholder}
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
            </div>
          )}

          {!isStreaming && recovery}

          <div ref={bottomRef} />
        </div>
      </div>

      {appError && <div className="app-notice">{appError}</div>}
      {sendNotice && <div className="app-notice">{sendNotice}</div>}

      <Composer
        isStreaming={isStreaming}
        preparing={preparing}
        canSend={canSend}
        messageCount={messages.length}
        onSend={onSend}
        onAbort={onAbort}
      />
    </div>
  )
}

interface ComposerProps {
  isStreaming: boolean
  preparing: boolean
  canSend: boolean
  // focus returns to the composer when a turn ends or the history changes
  messageCount: number
  onSend: (content: string) => Promise<boolean>
  onAbort: () => void
}

// the draft lives here, not in ChatView: a keystroke re-renders only the
// composer, never the transcript, whose markdown parse and code highlighting
// grow with the conversation. the draft survives conversation switches
// because ChatView is not keyed per conversation.
function Composer({ isStreaming, preparing, canSend, messageCount, onSend, onAbort }: ComposerProps) {
  const [input, setInput] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    resizeTextarea(textareaRef.current)
  }, [input])

  useEffect(() => {
    if (!isStreaming) {
      textareaRef.current?.focus()
    }
  }, [isStreaming, messageCount])

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void handleSend()
    }
  }

  const handleSend = async () => {
    // the draft stays until the request has actually started: a refused
    // send, a failed pre-send save, or a rejected request leaves the typed
    // text where the reader left it.
    if (!input.trim() || isStreaming || preparing || !canSend) return
    const content = input
    if (await onSend(content)) setInput(current => current === content ? '' : current)
  }

  return (
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
        disabled={isStreaming || preparing}
      />
      {isStreaming ? (
        <button className="send-btn" onClick={onAbort}>Stop</button>
      ) : (
        <button
          className="send-btn"
          onClick={handleSend}
          disabled={!input.trim() || !canSend || preparing}
        >
          Send
        </button>
      )}
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
