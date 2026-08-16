interface Props {
  thinking: string
  collapsed: boolean
  streaming: boolean
  onToggle?: (collapsed: boolean) => void
}

const COLLAPSED_FRAGMENT_LIMIT = 80

export function ThinkingBlock({ thinking, collapsed, streaming, onToggle }: Props) {
  const interactive = !streaming && !!onToggle
  const isCollapsed = collapsed && !streaming

  return (
    <div className={`thinking-block${isCollapsed ? ' thinking-collapsed' : ''}`}>
      <div
        className={`thinking-block-header${interactive ? ' thinking-interactive' : ''}`}
        onClick={interactive ? () => onToggle!(!collapsed) : undefined}
      >
        <span className="thinking-block-label">thinking</span>
        {isCollapsed && (
          <span className="thinking-block-fragment">{openingFragment(thinking)}</span>
        )}
        {interactive && (
          <span className="thinking-block-toggle" aria-hidden="true">
            {collapsed ? 'show' : 'hide'}
          </span>
        )}
      </div>

      {!isCollapsed && (
        <pre className="thinking-block-text">{thinking}</pre>
      )}
    </div>
  )
}

// collapsed state is one line: the label plus a truncated fragment of the
// block's opening — first non-empty line, elided past the limit
function openingFragment(thinking: string): string {
  const line = thinking
    .split('\n')
    .map(l => l.trim())
    .find(l => l.length > 0)
  const opening = line ?? thinking.trim()
  if (opening.length <= COLLAPSED_FRAGMENT_LIMIT) {
    return opening
  }
  return `${opening.slice(0, COLLAPSED_FRAGMENT_LIMIT)}...`
}