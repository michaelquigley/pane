export interface ParsedSSEEvent {
  type: string
  data: string
}

interface EventBoundary {
  start: number
  end: number
}

export function createSSEParser() {
  let buffer = ''

  return {
    push(chunk: string): ParsedSSEEvent[] {
      buffer += chunk

      const events: ParsedSSEEvent[] = []
      while (true) {
        const boundary = findEventBoundary(buffer)
        if (!boundary) break

        const block = buffer.slice(0, boundary.start)
        buffer = buffer.slice(boundary.end)

        const event = parseSSEBlock(block)
        if (event) events.push(event)
      }

      return events
    },
  }
}

function findEventBoundary(buffer: string): EventBoundary | null {
  const candidates = [
    findBoundary(buffer, '\r\n\r\n'),
    findBoundary(buffer, '\n\n'),
    findBoundary(buffer, '\r\r'),
  ].filter((boundary): boundary is EventBoundary => boundary !== null)

  if (candidates.length === 0) return null

  return candidates.reduce((earliest, boundary) => (
    boundary.start < earliest.start ? boundary : earliest
  ))
}

function findBoundary(buffer: string, separator: string): EventBoundary | null {
  const start = buffer.indexOf(separator)
  if (start === -1) return null
  return { start, end: start + separator.length }
}

function parseSSEBlock(block: string): ParsedSSEEvent | null {
  let type = ''
  const data: string[] = []

  for (const rawLine of block.split(/\r\n|\r|\n/)) {
    if (!rawLine || rawLine.startsWith(':')) continue

    const separator = rawLine.indexOf(':')
    const field = separator === -1 ? rawLine : rawLine.slice(0, separator)
    let value = separator === -1 ? '' : rawLine.slice(separator + 1)
    if (value.startsWith(' ')) value = value.slice(1)

    switch (field) {
      case 'event':
        type = value
        break
      case 'data':
        data.push(value)
        break
    }
  }

  if (!type || data.length === 0) return null

  return {
    type,
    data: data.join('\n'),
  }
}
