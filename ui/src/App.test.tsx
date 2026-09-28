// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import { EventStream, FakeBackend } from './test/fakeBackend'
import type { Conversation, Message, SystemPromptMode, TurnRecord } from './types'
import { validateRecovery, type ChatRequestBody } from './lib/turnRecord'

let backend: FakeBackend

beforeEach(() => {
  backend = new FakeBackend()
  vi.stubGlobal('fetch', backend.fetch)
  localStorage.clear()
  Element.prototype.scrollIntoView = () => {}
  Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

function conversation(title: string, messages: Message[], updatedAt = 1000, turns?: TurnRecord[]): Conversation {
  const doc: Conversation = { title, messages, createdAt: 1, updatedAt }
  if (turns) doc.turns = turns
  return doc
}

const priorExchange: Message[] = [
  { role: 'user', content: 'first question' },
  { role: 'assistant', content: 'first answer' },
]

function seedActive(id = 'c1') {
  backend.seed('c1', conversation('first', priorExchange, 2000))
  backend.seed('c2', conversation('second', [{ role: 'user', content: 'other' }, { role: 'assistant', content: 'other answer' }], 1000))
  localStorage.setItem('pane:activeConversation', JSON.stringify(id))
}

async function mount() {
  const view = render(<App />)
  await waitFor(() => expect(backend.modelFetches).toBeGreaterThan(0))
  return view
}

const composer = () => screen.getByPlaceholderText('Send a message...') as HTMLTextAreaElement
const sendButton = () => screen.getByRole('button', { name: 'Send' }) as HTMLButtonElement
const modelSelect = () => screen.getByLabelText('model') as HTMLSelectElement

function type(text: string) {
  fireEvent.change(composer(), { target: { value: text } })
}

async function send(text: string) {
  type(text)
  await waitFor(() => expect(sendButton().disabled).toBe(false))
  fireEvent.click(sendButton())
}

// invoke a React handler directly, bypassing a disabled control: proves the
// handler itself refuses, not just the button.
function invoke(element: Element, handler: string) {
  const key = Object.keys(element).find(k => k.startsWith('__reactProps$'))!
  const props = (element as unknown as Record<string, Record<string, (event: unknown) => void>>)[key]
  act(() => props[handler]({ stopPropagation() {}, preventDefault() {}, target: element }))
}

// drives one POST's lifecycle the way the backend's sink numbers it.
class Driver {
  seq = 0
  readonly stream: EventStream
  readonly turnId: string
  constructor(post: FakeBackend['chatPosts'][number]) {
    this.stream = post.stream
    this.turnId = post.body.turn_id as string
  }
  critical(type: string, data: Record<string, unknown>) {
    act(() => this.stream.send(type, { turn_id: this.turnId, seq: ++this.seq, ...data }))
  }
  start() {
    this.critical('turn_start', { alias: 'qwen', origin: { alias: 'qwen', identity: { provider: 'openai-chat-completions', protocol: 'chat-completions', upstream_model: 'q', service: 'http://q' } } })
  }
  assistant(round: number, content: string | null, calls: [string, string][] = [], extra: Record<string, unknown> = {}): Message {
    const message: Message = { role: 'assistant', content, turn_id: this.turnId, round_id: `${this.turnId}-r${round}`, ...extra }
    if (calls.length) message.tool_calls = calls.map(([id, name]) => ({ id, type: 'function', function: { name, arguments: '{}' } }))
    return message
  }
  ready(round: number, message: Message) {
    this.critical('round_ready', { round_id: `${this.turnId}-r${round}`, assistant: message, finish: message.tool_calls ? 'tool_calls' : 'stop' })
  }
  complete(round: number, message: Message, tools: Message[] = []) {
    this.critical('round_complete', { round_id: `${this.turnId}-r${round}`, assistant: message, tool_messages: tools })
  }
  end(outcome: string, execution: string) {
    this.critical('turn_end', { outcome, execution })
    act(() => this.stream.send('done', {}))
    act(() => this.stream.close())
  }
  simpleAnswer(text: string) {
    this.start()
    const message = this.assistant(1, text)
    this.ready(1, message)
    this.complete(1, message)
    this.end('completed', 'none')
  }
}

async function nextPost(count: number) {
  await waitFor(() => expect(backend.chatPosts).toHaveLength(count))
  return new Driver(backend.chatPosts[count - 1])
}

function savedTurns(id = 'c1'): TurnRecord[] {
  return backend.doc(id)?.turns ?? []
}

describe('pre-send save barrier', () => {
  it('a rejected candidate save sends nothing, keeps the draft, and never leaks into later saves', async () => {
    seedActive()
    backend.failSave = (_id, body) => body.includes('secret draft') ? 500 : null
    await mount()
    await send('secret draft')
    await screen.findByText(/not sent: the conversation could not be saved/)
    expect(backend.chatPosts).toHaveLength(0)
    expect(composer().value).toBe('secret draft')

    // a rename and an unrelated new conversation both save; neither carries
    // the rejected message or its marker.
    fireEvent.click(screen.getByRole('button', { name: 'conversations' }))
    fireEvent.click(screen.getByRole('button', { name: 'rename conversation first' }))
    const input = document.querySelector('.conversation-title-input') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'renamed' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    fireEvent.click(screen.getByRole('button', { name: 'new conversation' }))
    await waitFor(() => expect(backend.puts.length).toBeGreaterThanOrEqual(3))

    for (const put of backend.puts.slice(1)) {
      expect(put.body).not.toContain('secret draft')
      expect(put.body).not.toContain('in_progress')
    }
    expect(backend.doc('c1')?.title).toBe('renamed')
    expect(JSON.stringify(backend.doc('c1'))).not.toContain('secret draft')
    expect(backend.chatPosts).toHaveLength(0)
  })

  it('holds ownership during a delayed save, then installs and posts exactly once for the original conversation', async () => {
    seedActive()
    await mount()
    backend.holdSaves = true
    await send('hello')
    await waitFor(() => expect(backend.heldSaves).toBe(1))

    // controls are locked...
    expect(modelSelect().disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'new conversation' }) as HTMLButtonElement).disabled).toBe(true)
    expect(composer().disabled).toBe(true)
    // ...and the handlers refuse even when reached directly.
    fireEvent.click(screen.getByRole('button', { name: 'conversations' }))
    fireEvent.click(screen.getByText('second'))
    const row = screen.getByText('first').closest('.conversation-item')!
    invoke(within(row as HTMLElement).getByText('×'), 'onClick')
    invoke(screen.getByRole('button', { name: 'new conversation' }), 'onClick')
    invoke(screen.getByRole('button', { name: 'New conversation' }), 'onClick')
    fireEvent.change(modelSelect(), { target: { value: 'sol' } })
    expect(backend.deletes).toEqual([])
    expect(backend.chatPosts).toHaveLength(0)
    expect(JSON.parse(localStorage.getItem('pane:activeConversation')!)).toBe('c1')
    expect(backend.puts).toHaveLength(1)

    backend.holdSaves = false
    act(() => backend.release(true))
    const driver = await nextPost(1)
    const post = backend.chatPosts[0].body as unknown as ChatRequestBody
    const candidate = JSON.parse(backend.puts[0].body) as Conversation
    const marker = candidate.turns![0]
    expect(backend.puts[0].id).toBe('c1')
    expect(marker).toMatchObject({ id: post.turn_id, state: 'in_progress', model_alias: 'qwen', user_message_index: 2 })
    expect(candidate.messages[2]).toEqual({ role: 'user', content: 'hello', turn_id: post.turn_id })
    expect(post.messages).toHaveLength(3)
    expect(post.messages[2]).toEqual(candidate.messages[2])
    expect(post.recovery).toEqual({ v: 1, turns: [] })
    expect(post.model).toBe('qwen')
    expect(validateRecovery(post)).toBeNull()
    expect(backend.deletes).toEqual([])
    expect(JSON.parse(localStorage.getItem('pane:activeConversation')!)).toBe('c1')
    await waitFor(() => expect(composer().value).toBe(''))

    driver.simpleAnswer('hi there')
    await screen.findByText('hi there')
    await waitFor(() => expect(savedTurns()[0]?.state).toBe('completed'))
    expect(backend.chatPosts).toHaveLength(1)
    expect((screen.getByRole('button', { name: 'new conversation' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('a stale callback after teardown never posts; the written marker stays recoverable', async () => {
    seedActive()
    const view = await mount()
    backend.holdSaves = true
    await send('hello')
    await waitFor(() => expect(backend.heldSaves).toBe(1))
    view.unmount()
    backend.holdSaves = false
    await act(async () => { backend.release(true) })
    await new Promise(resolve => setTimeout(resolve, 20))
    expect(backend.chatPosts).toHaveLength(0)
    expect(savedTurns()[0].state).toBe('in_progress')

    await mount()
    await screen.findByRole('region', { name: 'interrupted turn' })
    type('another')
    expect(sendButton().disabled).toBe(true)
    expect(screen.getByText(/record its outcome below before sending/)).toBeDefined()
  })

  it('creates a new conversation only after its candidate is acknowledged', async () => {
    await mount()
    backend.holdSaves = true
    await send('brand new')
    await waitFor(() => expect(backend.heldSaves).toBe(1))
    expect(document.querySelector('.messages')!.textContent).not.toContain('brand new')
    expect(backend.store.size).toBe(0)
    backend.holdSaves = false
    act(() => backend.release(true))
    const driver = await nextPost(1)
    const id = backend.puts[0].id
    expect(JSON.parse(localStorage.getItem('pane:activeConversation')!)).toBe(id)
    driver.simpleAnswer('welcome')
    await screen.findByText('welcome')
    await waitFor(() => expect(savedTurns(id)[0]?.state).toBe('completed'))
    expect(backend.doc(id)?.messages.map(m => m.content)).toEqual(['brand new', 'welcome'])
  })
})

describe('prepared settings', () => {
  for (const mode of ['default', 'custom', 'none'] as SystemPromptMode[]) {
    it(`fixes the model and '${mode}' prompt captured before the save`, async () => {
      backend.models.push({ id: 'qwen@low', object: 'model', owned_by: 'pane', provider: 'openai-chat-completions', auth_state: 'not_required' })
      localStorage.setItem('pane:chatPreferences', JSON.stringify({ modelOverride: 'qwen', systemPromptMode: mode, systemPromptCustom: 'prompt A' }))
      seedActive()
      await mount()
      // the editor is already open when preparation starts.
      fireEvent.click(screen.getByRole('button', { name: `system prompt (${mode})` }))
      const modeSelect = document.querySelector('.system-prompt-mode') as HTMLSelectElement
      backend.holdSaves = true
      await send('hi')
      await waitFor(() => expect(backend.heldSaves).toBe(1))

      fireEvent.change(modelSelect(), { target: { value: 'qwen@low' } })
      expect(modeSelect.disabled).toBe(true)
      fireEvent.change(modeSelect, { target: { value: mode === 'none' ? 'custom' : 'none' } })
      const textarea = document.querySelector('.system-prompt-textarea') as HTMLTextAreaElement | null
      if (textarea) fireEvent.change(textarea, { target: { value: 'prompt B' } })
      expect(backend.chatPosts).toHaveLength(0)

      backend.holdSaves = false
      act(() => backend.release(true))
      const driver = await nextPost(1)
      const post = backend.chatPosts[0].body
      expect(post.model).toBe('qwen')
      expect(post.system_prompt_mode).toBe(mode)
      if (mode === 'custom') expect(post.system_prompt).toBe('prompt A')
      else expect(post.system_prompt).toBeUndefined()
      const candidate = JSON.parse(backend.puts[0].body) as Conversation
      expect(candidate.turns![0]).toMatchObject({ id: post.turn_id, model_alias: 'qwen' })
      expect(candidate.messages.at(-1)).toEqual({ role: 'user', content: 'hi', turn_id: post.turn_id })

      // the model stays fixed through the active turn...
      fireEvent.change(modelSelect(), { target: { value: 'qwen@low' } })
      expect(JSON.parse(localStorage.getItem('pane:chatPreferences')!).modelOverride).toBe('qwen')
      driver.simpleAnswer('done')
      await screen.findByText('done')
      // ...and an explicit selection afterwards affects only the next turn.
      await waitFor(() => expect(modelSelect().disabled).toBe(false))
      fireEvent.change(modelSelect(), { target: { value: 'qwen@low' } })
      expect(JSON.parse(localStorage.getItem('pane:chatPreferences')!).modelOverride).toBe('qwen@low')
      expect(backend.chatPosts).toHaveLength(1)
    })
  }
})

describe('interruption and recovery', () => {
  it('a disconnect before any tool event still requires reconciliation', async () => {
    seedActive()
    await mount()
    await send('go')
    const driver = await nextPost(1)
    driver.start()
    act(() => driver.stream.close())
    await screen.findByText('connection lost')
    await waitFor(() => expect(savedTurns()[0]?.state).toBe('interrupted'))
    expect(savedTurns()[0].terminal).toBeUndefined()
    await screen.findByRole('region', { name: 'interrupted turn' })
    type('next')
    expect(sendButton().disabled).toBe(true)
  })

  it('keeps a received result beside an unknown outcome, reconciles, and continues without repeating', async () => {
    seedActive()
    await mount()
    await send('read then write')
    const driver = await nextPost(1)
    driver.start()
    const round = driver.assistant(1, null, [['c1', 'read'], ['c2', 'write']])
    driver.ready(1, round)
    const putsBefore = backend.puts.length
    driver.critical('tool_call_executing', { round_id: `${driver.turnId}-r1`, index: 0, id: 'c1', name: 'read' })
    // a recovery-only change is mirrored on its own.
    await waitFor(() => expect(backend.puts.length).toBeGreaterThan(putsBefore))
    driver.critical('tool_call_result', { round_id: `${driver.turnId}-r1`, index: 0, id: 'c1', name: 'read', status: 'complete', content: '42', duration_ms: 1, execution_state: 'result_received' })
    driver.critical('tool_call_executing', { round_id: `${driver.turnId}-r1`, index: 1, id: 'c2', name: 'write' })
    driver.critical('tool_call_result', { round_id: `${driver.turnId}-r1`, index: 1, id: 'c2', name: 'write', status: 'error', content: 'error: disconnected', duration_ms: 1, execution_state: 'unknown', error_code: 'execution_error' })
    act(() => driver.stream.send('error', { turn_id: driver.turnId, code: 'tool_outcome_unknown', message: "tool outcome unknown for 'write'" }))
    driver.critical('turn_end', { outcome: 'failed', execution: 'unknown', error_code: 'tool_outcome_unknown' })
    act(() => driver.stream.close())

    await waitFor(() => expect(savedTurns()[0]?.terminal?.execution).toBe('unknown'))
    const calls = savedTurns()[0].rounds[0].calls
    expect(calls.map(call => call.state)).toEqual(['completed', 'unknown'])
    expect(calls[0].result).toEqual({ content: '42', is_error: false })

    const panel = await screen.findByRole('region', { name: 'interrupted turn' })
    expect(within(panel).queryByRole('button', { name: 'retry' })).toBeNull()
    fireEvent.change(within(panel).getByLabelText('note for c2'), { target: { value: 'cannot tell if it wrote.' } })
    fireEvent.click(within(panel).getByRole('button', { name: 'record and continue' }))
    await waitFor(() => expect(savedTurns()[0]?.reconciliation?.execution).toBe('unknown'))

    await send('what remains uncertain?')
    await nextPost(2)
    const post = backend.chatPosts[1].body as unknown as ChatRequestBody
    expect(validateRecovery(post)).toBeNull()
    expect(post.messages.slice(3).map(m => [m.role, m.recovery_placeholder, m.content])).toEqual([
      ['assistant', undefined, null],
      ['tool', undefined, '42'],
      ['tool', 'unknown', 'pane recovery: execution outcome unknown; no result was received.\noperator note: cannot tell if it wrote.'],
      ['user', undefined, 'operator reconciliation: c2: cannot tell if it wrote.'],
      ['user', undefined, 'what remains uncertain?'],
    ])
    expect(post.recovery.turns.map(turn => turn.id)).toEqual([driver.turnId])
    expect(post.recovery.turns[0].rounds[0].calls.map(call => call.state)).toEqual(['completed', 'unknown'])
    expect(backend.chatPosts).toHaveLength(2)
  })

  it('a lost terminal save leaves a conservative marker after reload', async () => {
    seedActive()
    backend.failSave = (_id, body) => body.includes('"state":"completed"') ? 500 : null
    const view = await mount()
    await send('hello')
    ;(await nextPost(1)).simpleAnswer('hi')
    await screen.findByText('hi')
    await waitFor(() => expect(backend.puts.some(put => put.body.includes('"state":"completed"'))).toBe(true))
    expect(savedTurns()[0].state).toBe('in_progress')
    view.unmount()
    backend.failSave = null
    await mount()
    await screen.findByRole('region', { name: 'interrupted turn' })
  })

  it('a saved terminal outcome reloads clean', async () => {
    seedActive()
    const view = await mount()
    await send('hello')
    ;(await nextPost(1)).simpleAnswer('hi')
    await waitFor(() => expect(savedTurns()[0]?.state).toBe('completed'))
    view.unmount()
    await mount()
    await screen.findByText('hi')
    expect(screen.queryByRole('region', { name: 'interrupted turn' })).toBeNull()
  })

  it('a lifecycle gap stops consumption and interrupts the turn', async () => {
    seedActive()
    await mount()
    await send('go')
    const driver = await nextPost(1)
    driver.start()
    driver.seq++
    driver.ready(1, driver.assistant(1, 'skipped a sequence number'))
    await screen.findByText(/recovery information was lost/)
    expect(backend.chatPosts[0].signal?.aborted).toBe(true)
    await waitFor(() => expect(savedTurns()[0]?.state).toBe('interrupted'))
    expect(savedTurns()[0].rounds).toEqual([])
  })

  it('stop interrupts the turn and later events cannot mutate it', async () => {
    seedActive()
    await mount()
    await send('go')
    const driver = await nextPost(1)
    driver.start()
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }))
    driver.ready(1, driver.assistant(1, 'late'))
    await waitFor(() => expect(savedTurns()[0]?.state).toBe('interrupted'))
    expect(savedTurns()[0].rounds).toEqual([])
    expect(screen.queryByText('late')).toBeNull()
  })

  it('switching conversations mid-turn interrupts the owner and leaves the other untouched', async () => {
    seedActive()
    await mount()
    await send('go')
    const driver = await nextPost(1)
    driver.start()
    driver.ready(1, driver.assistant(1, null, [['c1', 'write']]))
    await waitFor(() => expect(savedTurns('c1')[0]?.rounds).toHaveLength(1))
    fireEvent.click(screen.getByRole('button', { name: 'conversations' }))
    fireEvent.click(screen.getByText('second'))
    await screen.findByText('other answer')
    await waitFor(() => expect(savedTurns('c1')[0]?.state).toBe('interrupted'))
    expect(savedTurns('c1')[0].rounds[0].calls[0].state).toBe('pending')
    await new Promise(resolve => setTimeout(resolve, 20))
    expect(backend.puts.filter(put => put.id === 'c2')).toEqual([])
    expect(backend.doc('c2')?.turns).toBeUndefined()
    expect(backend.doc('c2')?.messages.map(m => m.content)).toEqual(['other', 'other answer'])
  })

  it('stop keeps the streamed answer as display-only partial text through reload, and never sends it', async () => {
    seedActive()
    const view = await mount()
    await send('go')
    const driver = await nextPost(1)
    driver.start()
    act(() => driver.stream.send('delta', { turn_id: driver.turnId, round_id: `${driver.turnId}-r1`, content: 'PARTIAL-ANSWER' }))
    await screen.findByText('PARTIAL-ANSWER')
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }))
    await waitFor(() => expect(savedTurns()[0]?.partial_text).toBe('PARTIAL-ANSWER'))
    expect(savedTurns()[0].state).toBe('interrupted')
    expect(backend.doc('c1')!.messages).toHaveLength(3)

    // late events on the stopped request change neither the record nor the display.
    act(() => driver.stream.send('delta', { turn_id: driver.turnId, round_id: `${driver.turnId}-r1`, content: 'LATE-DELTA' }))
    driver.ready(1, driver.assistant(1, 'LATE-ROUND'))
    await new Promise(resolve => setTimeout(resolve, 20))
    expect(screen.queryByText(/LATE-/)).toBeNull()
    expect(savedTurns()[0].rounds).toEqual([])
    expect(savedTurns()[0].partial_text).toBe('PARTIAL-ANSWER')

    view.unmount()
    await mount()
    const panel = await screen.findByRole('region', { name: 'interrupted turn' })
    expect(panel.textContent).toContain('not recorded as model output: PARTIAL-ANSWER')
    fireEvent.change(within(panel).getByLabelText('turn outcome'), { target: { value: 'none' } })
    fireEvent.change(within(panel).getByLabelText('reconciliation note'), { target: { value: 'stopped while answering.' } })
    fireEvent.click(within(panel).getByRole('button', { name: 'record and continue' }))
    await send('next')
    await nextPost(2)
    expect(JSON.stringify(backend.chatPosts[1].body)).not.toContain('PARTIAL-ANSWER')
    expect(backend.doc('c1')!.messages.some(m => m.content === 'PARTIAL-ANSWER')).toBe(false)
  })

  it('stopping in a later round keeps only that round\'s partial text', async () => {
    seedActive()
    await mount()
    await send('go')
    const driver = await nextPost(1)
    driver.start()
    act(() => driver.stream.send('delta', { turn_id: driver.turnId, round_id: `${driver.turnId}-r1`, content: 'FIRST-ANSWER' }))
    const first = driver.assistant(1, 'FIRST-ANSWER', [['c1', 'read']])
    driver.ready(1, first)
    driver.critical('tool_call_executing', { round_id: `${driver.turnId}-r1`, index: 0, id: 'c1', name: 'read' })
    driver.critical('tool_call_result', { round_id: `${driver.turnId}-r1`, index: 0, id: 'c1', name: 'read', status: 'complete', content: '42', duration_ms: 1, execution_state: 'result_received' })
    driver.complete(1, first, [{ role: 'tool', content: '42', tool_call_id: 'c1', turn_id: driver.turnId, round_id: `${driver.turnId}-r1` }])
    act(() => driver.stream.send('delta', { turn_id: driver.turnId, round_id: `${driver.turnId}-r2`, content: 'SECOND-PARTIAL' }))
    await screen.findByText('SECOND-PARTIAL')
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }))
    await waitFor(() => expect(savedTurns()[0]?.partial_text).toBe('SECOND-PARTIAL'))
    const messages = backend.doc('c1')!.messages
    expect(messages.filter(m => m.content === 'FIRST-ANSWER')).toHaveLength(1)
    expect(messages.some(m => m.content?.includes('SECOND-PARTIAL'))).toBe(false)
    expect(savedTurns()[0].rounds[0].committed).toBe(true)
  })

  it('switching conversations mid-answer keeps the partial text on the owning turn', async () => {
    seedActive()
    await mount()
    await send('go')
    const driver = await nextPost(1)
    driver.start()
    act(() => driver.stream.send('delta', { turn_id: driver.turnId, round_id: `${driver.turnId}-r1`, content: 'SWITCH-PARTIAL' }))
    await screen.findByText('SWITCH-PARTIAL')
    fireEvent.click(screen.getByRole('button', { name: 'conversations' }))
    fireEvent.click(screen.getByText('second'))
    await screen.findByText('other answer')
    await waitFor(() => expect(savedTurns('c1')[0]?.partial_text).toBe('SWITCH-PARTIAL'))
    expect(savedTurns('c1')[0].state).toBe('interrupted')
    expect(backend.doc('c2')?.turns).toBeUndefined()
    expect(JSON.stringify(backend.doc('c2'))).not.toContain('SWITCH-PARTIAL')
  })

  it('retries a typed pre-stream refusal as a new turn', async () => {
    seedActive()
    backend.chatRejection = { status: 503, code: 'login_required', message: "run 'pane auth login openai'" }
    await mount()
    await send('hello')
    await screen.findByText("run 'pane auth login openai'")
    await waitFor(() => expect(savedTurns()[0]?.terminal).toEqual({ outcome: 'failed', execution: 'none' }))
    backend.chatRejection = null
    fireEvent.click(await screen.findByRole('button', { name: 'retry' }))
    await nextPost(2)
    const post = backend.chatPosts[1].body as unknown as ChatRequestBody
    expect(post.turn_id).not.toBe(backend.chatPosts[0].body.turn_id)
    expect(post.messages.filter(m => m.role === 'user' && m.content === 'hello')).toHaveLength(2)
    expect(post.recovery.turns).toHaveLength(1)
    expect(validateRecovery(post)).toBeNull()
  })

  it('keeps continuation through save and the next request without thinking text', async () => {
    seedActive()
    await mount()
    await send('hello')
    const driver = await nextPost(1)
    const continuation = { format: 'codex-responses-items', v: 1, identity: { provider: 'openai-codex' }, items: [{ type: 'reasoning', id: 'rs_1', summary: [], encrypted_content: 'ENC' }, { type: 'message', id: 'm1' }] }
    driver.start()
    const message = driver.assistant(1, 'answer', [], { continuation })
    driver.ready(1, message)
    driver.complete(1, message)
    driver.end('completed', 'none')
    await waitFor(() => expect(savedTurns()[0]?.state).toBe('completed'))
    const saved = backend.doc('c1')!.messages[3]
    expect(saved.continuation).toEqual(continuation)
    expect(saved.thinking).toBeUndefined()
    await send('again')
    await nextPost(2)
    expect((backend.chatPosts[1].body.messages as Message[])[3].continuation).toEqual(continuation)
  })

  it('saves display thinking but never sends it', async () => {
    seedActive()
    await mount()
    await send('hello')
    const driver = await nextPost(1)
    driver.start()
    act(() => driver.stream.send('thinking_delta', { turn_id: driver.turnId, round_id: `${driver.turnId}-r1`, content: 'CANARY-THINKING' }))
    const message = driver.assistant(1, 'answer')
    driver.ready(1, message)
    driver.complete(1, message)
    driver.end('completed', 'none')
    await waitFor(() => expect(savedTurns()[0]?.state).toBe('completed'))
    expect(backend.doc('c1')!.messages[3].thinking).toBe('CANARY-THINKING')
    expect(JSON.stringify(savedTurns())).not.toContain('CANARY-THINKING')
    await send('again')
    await nextPost(2)
    expect(JSON.stringify(backend.chatPosts[1].body)).not.toContain('CANARY-THINKING')
  })
})

describe('availability', () => {
  it('a signed-out selection stays selected, blocks sending, and updates on focus and poll', async () => {
    backend.models[1].auth_state = 'login_required'
    localStorage.setItem('pane:chatPreferences', JSON.stringify({ modelOverride: 'sol', systemPromptMode: 'default', systemPromptCustom: '' }))
    seedActive()
    // only the poll's interval is faked; the testing library's own waits
    // keep real timers.
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const flush = () => act(async () => { await new Promise(resolve => setTimeout(resolve, 5)) })
    await mount()
    await screen.findByText(/run 'pane auth login openai'/)
    type('hello')
    expect(sendButton().disabled).toBe(true)
    expect(modelSelect().value).toBe('sol')
    expect(composer().value).toBe('hello')

    act(() => { window.dispatchEvent(new Event('focus')) })
    await flush()
    expect(backend.modelFetches).toBe(2)
    act(() => { vi.advanceTimersByTime(5000) })
    await flush()
    expect(backend.modelFetches).toBe(3)
    // a hidden tab does not poll.
    Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true })
    act(() => { vi.advanceTimersByTime(5000) })
    await flush()
    expect(backend.modelFetches).toBe(3)
    Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })

    backend.models[1].auth_state = 'credential_available'
    act(() => { vi.advanceTimersByTime(5000) })
    await flush()
    expect(sendButton().disabled).toBe(false)
    const fetches = backend.modelFetches
    act(() => { vi.advanceTimersByTime(15000) })
    await flush()
    expect(backend.modelFetches).toBe(fetches)
    expect(backend.chatPosts).toHaveLength(0)
  })
})
