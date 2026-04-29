import { useState } from 'react'
import type { ActiveToolCall } from '../types'

interface Props {
  toolCall: ActiveToolCall
  onApprove?: (id: string) => void
  onDeny?: (id: string) => void
}

export function ToolCallBlock({ toolCall, onApprove, onDeny }: Props) {
  const needsApproval = toolCall.status === 'awaiting_approval' && !!toolCall.id
  const [argsExpanded, setArgsExpanded] = useState(false)
  const showDetails = needsApproval || argsExpanded
  const displayName = toolCall.name || 'calling tool...'

  const statusIndicator = () => {
    switch (toolCall.status) {
      case 'loading':
      case 'args_streaming':
      case 'executing':
        return <span className="tool-status-text">{toolStatusLabel(toolCall.status)}</span>
      case 'awaiting_approval':
        return <span className="tool-status-text awaiting">waiting for approval</span>
      case 'complete':
        return <span className="tool-status-check">&#10003;</span>
      case 'error':
        return <span className="tool-status-error">&#10007;</span>
      default:
        return null
    }
  }

  let formattedArgs = toolCall.argumentsSoFar
  try {
    if (formattedArgs) {
      formattedArgs = JSON.stringify(JSON.parse(formattedArgs), null, 2)
    }
  } catch {
    // keep raw if not valid JSON yet
  }

  return (
    <div className={`tool-call-block tool-call-${toolCall.status}`}>
      <div className="tool-call-header" onClick={() => setArgsExpanded(!argsExpanded)}>
        {statusIndicator()}
        <span className="tool-call-name">{displayName}</span>
        {toolCall.durationMs !== undefined && (
          <span className="tool-call-duration">{toolCall.durationMs}ms</span>
        )}
        {toolCall.result !== undefined && (
          <span className="tool-disclosure-action" aria-hidden="true" />
        )}
      </div>

      {needsApproval && (
        <div className="tool-call-approval">
          <button className="approve-btn" onClick={() => onApprove?.(toolCall.id!)}>Approve</button>
          <button className="deny-btn" onClick={() => onDeny?.(toolCall.id!)}>Deny</button>
        </div>
      )}

      {showDetails && formattedArgs && (
        <pre className="tool-call-args">{formattedArgs}</pre>
      )}

      {showDetails && toolCall.result !== undefined && (
        <div className="tool-call-result-section">
          <span className="tool-message-label">tool result</span>
          <pre className="tool-call-result">{toolCall.result}</pre>
        </div>
      )}
    </div>
  )
}

function toolStatusLabel(status: ActiveToolCall['status']): string {
  switch (status) {
    case 'loading':
      return 'preparing'
    case 'args_streaming':
      return 'reading'
    case 'executing':
      return 'working'
    default:
      return ''
  }
}
