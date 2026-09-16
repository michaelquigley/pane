import type { SSEEvent, UsageRecord } from '../types'

type UsageEvent = Extract<SSEEvent, { type: 'usage' }>

export function usageRecordFromEvent(
  event: UsageEvent,
  model: string,
  at: number = Date.now(),
): UsageRecord {
  return {
    promptTokens: event.prompt_tokens,
    completionTokens: event.completion_tokens,
    totalTokens: event.total_tokens,
    model,
    at,
  }
}
