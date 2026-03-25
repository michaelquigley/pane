import { useState } from 'react'

interface Props {
  value: string
  onChange: (value: string) => void
}

export function SystemPromptEditor({ value, onChange }: Props) {
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
        <textarea
          className="system-prompt-textarea"
          value={value}
          onChange={e => onChange(e.target.value)}
          placeholder="Override system prompt..."
          rows={3}
        />
      )}
    </div>
  )
}
