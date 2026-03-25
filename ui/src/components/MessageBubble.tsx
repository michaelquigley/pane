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
  activeToolCalls?: Map<string, ActiveToolCall>
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
  if (message.role === 'system' || message.role === 'tool') {
    return null
  }

  const isUser = message.role === 'user'
  const content = isStreaming ? (streamingContent || '') : (message.content || '')
  const toolCalls = isStreaming
    ? Array.from(activeToolCalls?.values() || [])
    : (message.tool_calls || []).map(tc => ({
        id: tc.id,
        name: tc.function.name,
        status: 'complete' as const,
        argumentsSoFar: tc.function.arguments,
        result: findToolResult(message, tc.id),
      }))

  return (
    <div className={`message ${isUser ? 'message-user' : 'message-assistant'}`}>
      {isUser ? (
        <div className="message-content user-content">{content}</div>
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

function findToolResult(message: Message, _toolCallId: string): string | undefined {
  // In completed messages, tool results are separate Message objects.
  // The parent component handles this by not showing tool messages directly.
  // For completed tool calls shown inline, we don't have the result here.
  void _toolCallId
  void message
  return undefined
}
