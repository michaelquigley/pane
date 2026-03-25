import type { Conversation } from '../types'

interface Props {
  conversations: Conversation[]
  activeId: string | null
  onSelect: (id: string) => void
  onNew: () => void
  onDelete: (id: string) => void
}

export function ConversationList({ conversations, activeId, onSelect, onNew, onDelete }: Props) {
  return (
    <div className="conversation-list">
      <button className="new-chat-btn" onClick={onNew}>New conversation</button>
      <div className="conversation-items">
        {conversations.map(c => (
          <div
            key={c.id}
            className={`conversation-item ${c.id === activeId ? 'active' : ''}`}
            onClick={() => onSelect(c.id)}
          >
            <span className="conversation-title">{c.title || 'New conversation'}</span>
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
