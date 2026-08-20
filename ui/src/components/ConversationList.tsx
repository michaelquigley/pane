import type { StoredConversation } from '../types'

interface Props {
  conversations: StoredConversation[]
  activeId: string | null
  canCreate: boolean
  onSelect: (id: string) => void
  onNew: () => void
  onDelete: (id: string) => void
}

const encoder = new TextEncoder()

// UTF-8 byte order, the same comparison the store's List applies. javascript's
// string relational operator orders by UTF-16 code units, which diverges from
// Go on supplementary-plane characters, and localeCompare is collation
// sensitive; neither can promise that screen order and disk order agree for
// every id the store accepts.
function compareIdBytes(a: string, b: string): number {
  const left = encoder.encode(a)
  const right = encoder.encode(b)
  const shared = Math.min(left.length, right.length)
  for (let i = 0; i < shared; i++) {
    if (left[i] !== right[i]) return left[i] - right[i]
  }
  return left.length - right.length
}

export function ConversationList({ conversations, activeId, canCreate, onSelect, onNew, onDelete }: Props) {
  // last activity first, so a commit that stamps updatedAt moves the
  // conversation to the top in the same render.
  const ordered = [...conversations].sort((a, b) =>
    b.doc.updatedAt - a.doc.updatedAt || compareIdBytes(a.id, b.id))

  return (
    <div className="conversation-list">
      <button className="new-chat-btn" onClick={onNew} disabled={!canCreate}>New conversation</button>
      <div className="conversation-items">
        {ordered.map(c => (
          <div
            key={c.id}
            className={`conversation-item ${c.id === activeId ? 'active' : ''}`}
            onClick={() => onSelect(c.id)}
          >
            <span className="conversation-title">{c.doc.title || 'New conversation'}</span>
            <button
              className="conversation-delete"
              onClick={e => { e.stopPropagation(); onDelete(c.id) }}
            >
              &times;
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}
