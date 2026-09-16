import { describe, expect, it } from 'vitest'
import { usageRecordFromEvent } from './usageRecord'

describe('usageRecordFromEvent', () => {
  it('keeps the selected pane alias as the usage identity', () => {
    const record = usageRecordFromEvent({
      type: 'usage',
      prompt_tokens: 42000,
      completion_tokens: 3000,
      total_tokens: 45000,
    }, 'qwen3.8-27b@fortyfive', 1234)

    expect(record).toEqual({
      promptTokens: 42000,
      completionTokens: 3000,
      totalTokens: 45000,
      model: 'qwen3.8-27b@fortyfive',
      at: 1234,
    })
  })
})
