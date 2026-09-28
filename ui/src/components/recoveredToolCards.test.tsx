// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { MessageBubble } from './MessageBubble'
import { interruptTurn, prepareTurn, reconcileTurn, type ChatRecord } from '../lib/turnRecord'
import type { Message } from '../types'

afterEach(cleanup)

// the reviewer's scenario: a read returns 42, a write's outcome is unknown and
// is reconciled as unknown, and a third call is operator-reported. rendered
// after a save/reload round trip.
function recoveredAssistant(): Message {
  const calls = ['read', 'write', 'edit'].map(id => ({ id, type: 'function' as const, function: { name: id, arguments: '{}' } }))
  const assistant: Message = { role: 'assistant', content: null, turn_id: 't1', round_id: 't1-r1', tool_calls: calls }
  const chat: ChatRecord = prepareTurn({ messages: [], turns: [] }, 'read then write', 't1', 'qwen')
  chat.turns[0] = { ...chat.turns[0], state: 'interrupted', terminal: { outcome: 'failed', execution: 'unknown' }, rounds: [{
    round_id: 't1-r1', assistant, committed: false, calls: [
      { ...calls[0], state: 'completed', result: { content: 'RECEIVED-42', is_error: false } },
      { ...calls[1], state: 'unknown' },
      { ...calls[2], state: 'dispatched' },
    ],
  }] }
  const recovered = reconcileTurn(interruptTurn(chat, 't1', ''), 't1', {
    write: { execution: 'unknown', note: 'cannot tell whether it wrote' },
    edit: { execution: 'known', note: 'the file shows the edit' },
  }, '', 'none')
  return (JSON.parse(JSON.stringify(recovered)) as ChatRecord).messages[1]
}

function card(name: string) {
  return screen.getByText(name).closest('.tool-call-block') as HTMLElement
}

describe('recovered tool cards', () => {
  it('show what pane knows: received results as results, placeholders as attributed notes', () => {
    render(<MessageBubble message={recoveredAssistant()} />)

    expect(card('read').querySelector('.tool-status-check')).not.toBeNull()
    fireEvent.click(screen.getByText('read'))
    expect(card('read').textContent).toContain('RECEIVED-42')
    expect(card('read').querySelector('.tool-message-label')?.textContent).toBe('tool result')

    for (const [name, marker] of [['write', 'outcome unknown'], ['edit', 'operator-reported']]) {
      const block = card(name)
      expect(block.querySelector('.tool-status-check')).toBeNull()
      expect(block.querySelector('.tool-status-error')).toBeNull()
      expect(block.textContent).toContain(marker)
      fireEvent.click(screen.getByText(name))
      expect(block.querySelector('.tool-message-label')?.textContent).toBe('pane recovery note')
    }
    expect(card('write').textContent).toContain('cannot tell whether it wrote')
    expect(card('edit').textContent).toContain('operator-reported.\noperator note: the file shows the edit')
  })
})
