// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { ChatView } from './ChatView'
import type { ActiveToolCall, Message } from '../types'

// each markdown body render is one parse plus its code highlighting -- the
// work that made typing slow as a conversation grew. the stand-in counts it.
const markdown = vi.hoisted(() => ({ renders: 0 }))
vi.mock('react-markdown', () => ({
  default: ({ children }: { children: string }) => {
    markdown.renders++
    return <div>{children}</div>
  },
}))

beforeAll(() => {
  Element.prototype.scrollIntoView = () => {}
})

afterEach(cleanup)

// index 0 is hidden, so a visible message's position differs from its
// index in the full history
const history: Message[] = [
  { role: 'system', content: 'be brief' },
  { role: 'user', content: 'first question' },
  { role: 'assistant', content: 'first answer', thinking: 'pondering the first' },
  { role: 'user', content: 'second question' },
  { role: 'assistant', content: 'second answer' },
]

// stable references, as App passes them: the transcript's memoization
// depends on the callbacks not changing between renders
const noToolCalls = new Map<number, ActiveToolCall>()
const onSend = async () => true
const onApprove = () => {}
const onDeny = () => {}
const onAbort = () => {}
const onToggleThinkingCollapsed = () => {}

function view(overrides: Partial<Parameters<typeof ChatView>[0]> = {}) {
  return (
    <ChatView
      messages={history}
      isStreaming={false}
      streamingContent=""
      streamingThinking=""
      activeToolCalls={noToolCalls}
      error={null}
      appError={null}
      canSend
      onSend={onSend}
      preparing={false}
      onApprove={onApprove}
      onDeny={onDeny}
      onAbort={onAbort}
      onToggleThinkingCollapsed={onToggleThinkingCollapsed}
      {...overrides}
    />
  )
}

describe('transcript rendering', () => {
  it('typing in the composer re-renders no markdown', () => {
    render(view())
    const composer = screen.getByPlaceholderText('Send a message...') as HTMLTextAreaElement
    markdown.renders = 0

    for (const draft of ['h', 'he', 'hel', 'hell', 'hello']) {
      fireEvent.change(composer, { target: { value: draft } })
    }

    expect(composer.value).toBe('hello')
    expect(markdown.renders).toBe(0)
  })

  it("a streaming text delta re-renders only the in-progress reply's markdown", () => {
    const { rerender } = render(view({ isStreaming: true, streamingContent: 'par' }))
    markdown.renders = 0

    rerender(view({ isStreaming: true, streamingContent: 'partial' }))

    expect(screen.getByText('partial')).toBeTruthy()
    expect(markdown.renders).toBe(1)
  })

  it('the thinking toggle addresses the message by its full-history index', () => {
    const toggle = vi.fn()
    render(view({ onToggleThinkingCollapsed: toggle }))

    fireEvent.click(screen.getByText('hide'))

    expect(toggle).toHaveBeenCalledWith(2, true)
  })
})
