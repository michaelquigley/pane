import type { UsageRecord } from '../types'

type ContextBand = 'neutral' | 'cool' | 'warm' | 'hot'

interface Props {
  usage: UsageRecord | null
  selectedModel: string
  contextWindows: Record<string, number>
  defaultContextWindow: number
}

interface ContextDisplay {
  value: string
  band: ContextBand
  title: string
}

export function ContextMeter({
  usage,
  selectedModel,
  contextWindows,
  defaultContextWindow,
}: Props) {
  const display = deriveContextDisplay(
    usage,
    selectedModel,
    contextWindows,
    defaultContextWindow,
  )

  return (
    <span
      className={`context-meter context-meter-${display.band}`}
      title={display.title}
      aria-label={display.title}
    >
      {display.value}
    </span>
  )
}

function deriveContextDisplay(
  usage: UsageRecord | null,
  selectedModel: string,
  contextWindows: Record<string, number>,
  defaultContextWindow: number,
): ContextDisplay {
  if (!usage) {
    return {
      value: '?',
      band: 'neutral',
      title: 'no usage reported yet',
    }
  }

  if (usage.model !== selectedModel) {
    return {
      value: '?',
      band: 'neutral',
      title: `usage was measured for \u0027${usage.model}\u0027, not selected model \u0027${selectedModel}\u0027`,
    }
  }

  const configuredWindow = contextWindows[usage.model]
  const contextWindow = configuredWindow > 0
    ? configuredWindow
    : defaultContextWindow > 0
      ? defaultContextWindow
      : 0

  if (contextWindow === 0) {
    return {
      value: '?',
      band: 'neutral',
      title: `no context window known for \u0027${usage.model}\u0027`,
    }
  }

  const percentage = Math.round(usage.promptTokens / contextWindow * 100)
  const band: ContextBand = percentage < 50
    ? 'cool'
    : percentage < 80
      ? 'warm'
      : 'hot'

  return {
    value: `${percentage}%`,
    band,
    title: `prompt context is \u0027${percentage}%\u0027 of \u0027${contextWindow}\u0027 tokens for \u0027${usage.model}\u0027`,
  }
}
