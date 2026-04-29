import type { Conversation, Message } from '../types'

export function conversationToMarkdown(conversation: Conversation): string {
  const title = conversationTitle(conversation)
  const parts = [
    `# ${title}`,
    '',
    `Created: ${formatLocalDateTime(conversation.createdAt)}`,
    `Updated: ${formatLocalDateTime(conversation.updatedAt)}`,
    '',
    '---',
    '',
  ]

  for (const message of exportableMessages(conversation.messages)) {
    parts.push(`## ${roleLabel(message.role)}`)
    parts.push('')
    parts.push(message.content ?? '')
    parts.push('')
  }

  return `${parts.join('\n').replace(/\n+$/, '')}\n`
}

export function hasExportableMessages(conversation: Conversation): boolean {
  return exportableMessages(conversation.messages).length > 0
}

export function buildConversationMarkdownFilename(conversation: Conversation): string {
  const slug = slugify(conversationTitle(conversation))
  return `pane-${slug}-${formatDateStamp(conversation.updatedAt)}.md`
}

export function downloadMarkdown(filename: string, markdown: string) {
  const blob = new Blob([markdown], { type: 'text/markdown;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 0)
}

function exportableMessages(messages: Message[]): Message[] {
  return messages.filter(message => {
    if (message.role !== 'user' && message.role !== 'assistant') return false
    return message.content !== null && message.content.length > 0
  })
}

function conversationTitle(conversation: Conversation): string {
  const title = conversation.title.replace(/\s+/g, ' ').trim()
  return title || 'Untitled conversation'
}

function roleLabel(role: Message['role']): string {
  return role === 'user' ? 'User' : 'Assistant'
}

function formatLocalDateTime(timestamp: number): string {
  return new Date(timestamp).toLocaleString()
}

function formatDateStamp(timestamp: number): string {
  const date = new Date(timestamp)
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function slugify(value: string): string {
  const slug = value
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 80)
    .replace(/-+$/g, '')

  return slug || 'conversation'
}
