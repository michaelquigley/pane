import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter'
import { oneDark } from 'react-syntax-highlighter/dist/esm/styles/prism'
import { ToolCallBlock } from './ToolCallBlock'
import type { Message, ActiveToolCall } from '../types'

interface Props {
  message: Message
  isStreaming?: boolean
  streamingContent?: string
  activeToolCalls?: Map<number, ActiveToolCall>
  onApprove?: (id: string) => void
  onDeny?: (id: string) => void
}

export function MessageBubble({
  message,
  isStreaming,
  streamingContent,
  activeToolCalls,
  onApprove,
  onDeny,
}: Props) {
  if (message.role === 'system') {
    return null
  }

  const isUser = message.role === 'user'
  const isTool = message.role === 'tool'
  const content = isStreaming ? (streamingContent || '') : (message.content || '')
  const toolCalls = isStreaming
    ? Array.from(activeToolCalls?.values() || [])
    : (message.tool_calls || []).map((tc, index) => ({
        ...(message.tool_call_results?.[tc.id] || {}),
        index,
        id: tc.id,
        name: tc.function.name,
        status: message.tool_call_results?.[tc.id]?.status || 'complete',
        argumentsSoFar: tc.function.arguments,
        result: message.tool_call_results?.[tc.id]?.content,
        durationMs: message.tool_call_results?.[tc.id]?.duration_ms,
        errorCode: message.tool_call_results?.[tc.id]?.error_code,
      }))

  return (
    <div className={`message ${isUser ? 'message-user' : isTool ? 'message-tool' : 'message-assistant'}`}>
      {isUser ? (
        <div className="message-content user-content">{content}</div>
      ) : isTool ? (
        <details className="message-content tool-content">
          <summary className="tool-disclosure-summary">
            <span className="tool-message-label">tool result</span>
            <span className="tool-disclosure-action" aria-hidden="true" />
          </summary>
          <pre>{content}</pre>
        </details>
      ) : (
        <>
          {toolCalls.length > 0 && (
            <div className="tool-calls">
              {toolCalls.map(tc => (
                <ToolCallBlock
                  key={tc.id}
                  toolCall={tc as ActiveToolCall}
                  onApprove={onApprove}
                  onDeny={onDeny}
                />
              ))}
            </div>
          )}
          {content && (
            <div className="message-content assistant-content">
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                components={{
                  code({ className, children, ...props }) {
                    const match = /language-(\w+)/.exec(className || '')
                    const code = String(children).replace(/\n$/, '')
                    if (match) {
                      return (
                        <SyntaxHighlighter
                          style={oneDark}
                          language={match[1]}
                          PreTag="div"
                        >
                          {code}
                        </SyntaxHighlighter>
                      )
                    }
                    return <code className={className} {...props}>{children}</code>
                  }
                }}
              >
                {content}
              </ReactMarkdown>
              {isStreaming && <span className="streaming-cursor" />}
            </div>
          )}
          {!content && isStreaming && toolCalls.length === 0 && (
            <div className="message-content assistant-content">
              <span className="streaming-cursor" />
            </div>
          )}
        </>
      )}
    </div>
  )
}
