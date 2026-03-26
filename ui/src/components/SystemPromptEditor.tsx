import { useState } from 'react'
import type { SystemPromptMode } from '../types'

interface Props {
  mode: SystemPromptMode
  customValue: string
  defaultValue: string
  onModeChange: (value: SystemPromptMode) => void
  onCustomChange: (value: string) => void
}

export function SystemPromptEditor({
  mode,
  customValue,
  defaultValue,
  onModeChange,
  onCustomChange,
}: Props) {
  const [expanded, setExpanded] = useState(false)

  return (
    <div className="system-prompt-editor">
      <button
        className="system-prompt-toggle"
        onClick={() => setExpanded(!expanded)}
      >
        System prompt {expanded ? '▾' : '▸'}
      </button>
      {expanded && (
        <div className="system-prompt-panel">
          <select
            className="system-prompt-mode"
            value={mode}
            onChange={e => onModeChange(e.target.value as SystemPromptMode)}
          >
            <option value="default">use default</option>
            <option value="custom">use custom</option>
            <option value="none">send none</option>
          </select>

          {mode === 'custom' ? (
            <textarea
              className="system-prompt-textarea"
              value={customValue}
              onChange={e => onCustomChange(e.target.value)}
              placeholder={defaultValue || 'Enter system prompt...'}
              rows={3}
            />
          ) : mode === 'none' ? (
            <div className="system-prompt-note">No system prompt will be sent.</div>
          ) : (
            <div className="system-prompt-note">
              {defaultValue || 'No configured system prompt.'}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
