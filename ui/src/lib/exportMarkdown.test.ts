import { describe, expect, it } from 'vitest'
import type { Conversation } from '../types'
import { conversationToMarkdown } from './exportMarkdown'

describe('markdown export', () => {
  it('excludes turn records, continuation, origin, placeholders, and thinking', () => {
    const doc: Conversation = {
      title: 'export',
      createdAt: 0,
      updatedAt: 0,
      messages: [
        { role: 'user', content: 'question', turn_id: 't1' },
        {
          role: 'assistant', content: 'answer', turn_id: 't1', round_id: 't1-r1', thinking: 'THINKING-CANARY',
          origin: { alias: 'sol', identity: { provider: 'openai-codex', protocol: 'responses', upstream_model: 'gpt-5.6-sol', service: 'svc', account_scope: 'chatgpt:SCOPE-CANARY' } },
          continuation: { items: [{ encrypted_content: 'ENCRYPTED-CANARY' }] },
        },
        { role: 'tool', content: 'pane recovery: PLACEHOLDER-CANARY', tool_call_id: 'c1', recovery_placeholder: 'unknown' },
      ],
      turns: [{ id: 't1', v: 1, model_alias: 'sol', user_message_index: 0, state: 'completed', last_seq: 3, rounds: [], partial_text: 'PARTIAL-CANARY' }],
    }
    const markdown = conversationToMarkdown(doc)
    expect(markdown).toContain('answer')
    for (const canary of ['THINKING-CANARY', 'SCOPE-CANARY', 'ENCRYPTED-CANARY', 'PLACEHOLDER-CANARY', 'PARTIAL-CANARY', 't1-r1']) {
      expect(markdown).not.toContain(canary)
    }
  })
})
