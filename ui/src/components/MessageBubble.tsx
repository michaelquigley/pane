import { lazy, Suspense } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { ToolCallBlock } from './ToolCallBlock'
import type { Message, ActiveToolCall } from '../types'

const MarkdownCodeBlock = lazy(() => import('./MarkdownCodeBlock'))

const latexSymbolReplacements: Record<string, string> = {
  '\\alpha': 'α',
  '\\beta': 'β',
  '\\gamma': 'γ',
  '\\delta': 'δ',
  '\\epsilon': 'ε',
  '\\zeta': 'ζ',
  '\\eta': 'η',
  '\\theta': 'θ',
  '\\iota': 'ι',
  '\\kappa': 'κ',
  '\\lambda': 'λ',
  '\\mu': 'μ',
  '\\nu': 'ν',
  '\\xi': 'ξ',
  '\\pi': 'π',
  '\\rho': 'ρ',
  '\\sigma': 'σ',
  '\\tau': 'τ',
  '\\upsilon': 'υ',
  '\\phi': 'φ',
  '\\chi': 'χ',
  '\\psi': 'ψ',
  '\\omega': 'ω',
  '\\Gamma': 'Γ',
  '\\Delta': 'Δ',
  '\\Theta': 'Θ',
  '\\Lambda': 'Λ',
  '\\Xi': 'Ξ',
  '\\Pi': 'Π',
  '\\Sigma': 'Σ',
  '\\Upsilon': 'Υ',
  '\\Phi': 'Φ',
  '\\Psi': 'Ψ',
  '\\Omega': 'Ω',
  '\\rightarrow': '→',
  '\\to': '→',
  '\\leftarrow': '←',
  '\\leftrightarrow': '↔',
  '\\longrightarrow': '⟶',
  '\\longleftarrow': '⟵',
  '\\longleftrightarrow': '⟷',
  '\\Rightarrow': '⇒',
  '\\Leftarrow': '⇐',
  '\\Leftrightarrow': '⇔',
  '\\uparrow': '↑',
  '\\downarrow': '↓',
  '\\updownarrow': '↕',
  '\\mapsto': '↦',
  '\\times': '×',
  '\\div': '÷',
  '\\cdot': '·',
  '\\pm': '±',
  '\\mp': '∓',
  '\\le': '≤',
  '\\leq': '≤',
  '\\ge': '≥',
  '\\geq': '≥',
  '\\ne': '≠',
  '\\neq': '≠',
  '\\approx': '≈',
  '\\equiv': '≡',
  '\\sim': '∼',
  '\\propto': '∝',
  '\\infty': '∞',
  '\\partial': '∂',
  '\\nabla': '∇',
  '\\forall': '∀',
  '\\exists': '∃',
  '\\in': '∈',
  '\\notin': '∉',
  '\\subset': '⊂',
  '\\subseteq': '⊆',
  '\\supset': '⊃',
  '\\supseteq': '⊇',
  '\\cup': '∪',
  '\\cap': '∩',
  '\\emptyset': '∅',
  '\\varnothing': '∅',
  '\\angle': '∠',
  '\\degree': '°',
}

interface Props {
  message: Message
  isStreaming?: boolean
  streamingContent?: string
  activeToolCalls?: Map<number, ActiveToolCall>
  compact?: boolean
  onApprove?: (id: string) => void
  onDeny?: (id: string) => void
}

export function MessageBubble({
  message,
  isStreaming,
  streamingContent,
  activeToolCalls,
  compact,
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
    <div className={`message ${isUser ? 'message-user' : 'message-assistant'}${compact ? ' message-compact' : ''}`}>
      {isUser ? (
        <div className="message-content user-content markdown-content">
          <MarkdownBody content={content} />
        </div>
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
            <div className="message-content assistant-content markdown-content">
              <MarkdownBody content={content} />
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

function MarkdownBody({ content }: { content: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        code({ className, children, ...props }) {
          const match = /language-(\w+)/.exec(className || '')
          const code = String(children).replace(/\n$/, '')
          if (match) {
            return (
              <Suspense fallback={<pre>{code}</pre>}>
                <MarkdownCodeBlock language={match[1]} code={code} />
              </Suspense>
            )
          }
          return <code className={className} {...props}>{children}</code>
        },
      }}
    >
      {replaceLatexSymbolSpans(content)}
    </ReactMarkdown>
  )
}

function replaceLatexSymbolSpans(content: string): string {
  let inFence: string | null = null

  return content
    .split('\n')
    .map(line => {
      const fenceMatch = /^(?: {0,3})(`{3,}|~{3,})/.exec(line)
      if (fenceMatch) {
        const fenceChar = fenceMatch[1][0]
        if (inFence === fenceChar) {
          inFence = null
        } else if (!inFence) {
          inFence = fenceChar
        }
        return line
      }

      if (inFence) {
        return line
      }

      return replaceLatexSymbolSpansOutsideInlineCode(line)
    })
    .join('\n')
}

function replaceLatexSymbolSpansOutsideInlineCode(line: string): string {
  let output = ''
  let index = 0

  while (index < line.length) {
    const codeStart = line.indexOf('`', index)
    if (codeStart === -1) {
      output += replaceLatexSymbolSpansInText(line.slice(index))
      break
    }

    output += replaceLatexSymbolSpansInText(line.slice(index, codeStart))

    let delimiterLength = 1
    while (line[codeStart + delimiterLength] === '`') {
      delimiterLength += 1
    }

    const delimiter = '`'.repeat(delimiterLength)
    const codeEnd = line.indexOf(delimiter, codeStart + delimiterLength)
    if (codeEnd === -1) {
      output += line.slice(codeStart)
      break
    }

    output += line.slice(codeStart, codeEnd + delimiterLength)
    index = codeEnd + delimiterLength
  }

  return output
}

function replaceLatexSymbolSpansInText(text: string): string {
  return text.replace(/\$([^$\n]+)\$/g, (match, expression: string) => {
    return renderSimpleLatexExpression(expression) ?? match
  })
}

function normalizeLatexSymbol(symbol: string): string {
  return symbol.trim().replace(/^\\+/, '\\')
}

function renderSimpleLatexExpression(expression: string): string | null {
  let output = ''
  let index = 0

  while (index < expression.length) {
    const remaining = expression.slice(index)
    const whitespaceMatch = /^\s+/.exec(remaining)
    if (whitespaceMatch) {
      output += whitespaceMatch[0]
      index += whitespaceMatch[0].length
      continue
    }

    const textMatch = /^\\text\{([^{}]*)\}/.exec(remaining)
    if (textMatch) {
      output += textMatch[1]
      index += textMatch[0].length
      continue
    }

    const symbolMatch = /^\\[A-Za-z]+/.exec(remaining)
    if (symbolMatch) {
      const replacement = latexSymbolReplacements[normalizeLatexSymbol(symbolMatch[0])]
      if (!replacement) {
        return null
      }
      output += replacement
      index += symbolMatch[0].length
      continue
    }

    return null
  }

  return output.trim().length > 0 ? output : null
}
