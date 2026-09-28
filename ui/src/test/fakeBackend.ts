import type { Conversation, ModelInfo } from '../types'

// a controllable SSE response body.
export class EventStream {
  private controller!: ReadableStreamDefaultController<Uint8Array>
  private encoder = new TextEncoder()
  readonly stream: ReadableStream<Uint8Array>
  closed = false

  constructor() {
    this.stream = new ReadableStream<Uint8Array>({ start: controller => { this.controller = controller } })
  }

  send(type: string, data: unknown) {
    if (this.closed) return
    this.controller.enqueue(this.encoder.encode(`event: ${type}\ndata: ${JSON.stringify(data)}\n\n`))
  }

  raw(text: string) {
    if (!this.closed) this.controller.enqueue(this.encoder.encode(text))
  }

  close() {
    if (this.closed) return
    this.closed = true
    this.controller.close()
  }
}

export interface Deferred {
  resolve: () => void
  reject: (status?: number) => void
}

// a single-process stand-in for pane's HTTP surface. every browser request
// goes through `fetch`; saves can be held or failed, and each chat POST
// gets its own controllable stream.
export class FakeBackend {
  store = new Map<string, string>()
  puts: { id: string; body: string }[] = []
  deletes: string[] = []
  chatPosts: { body: Record<string, unknown>; stream: EventStream; signal?: AbortSignal | null }[] = []
  modelFetches = 0
  models: ModelInfo[] = [
    { id: 'qwen', object: 'model', owned_by: 'pane', provider: 'openai-chat-completions', auth_state: 'not_required' },
    { id: 'sol', object: 'model', owned_by: 'pane', provider: 'openai-codex', auth_state: 'credential_available' },
  ]
  // held saves: a PUT waits until the test settles it.
  private holds: ((settle: { ok: boolean; status: number }) => void)[] = []
  holdSaves = false
  failSave: ((id: string, body: string) => number | null) | null = null
  chatRejection: { status: number; code: string; message: string } | null = null

  seed(id: string, doc: Conversation) {
    this.store.set(id, JSON.stringify(doc))
  }

  doc(id: string): Conversation | null {
    const body = this.store.get(id)
    return body ? JSON.parse(body) as Conversation : null
  }

  // settle the oldest held save.
  release(ok = true, status = 500) {
    const next = this.holds.shift()
    if (!next) throw new Error('no held save')
    next({ ok, status: ok ? 204 : status })
  }

  get heldSaves() {
    return this.holds.length
  }

  fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.pathname : input.url
    const method = init?.method ?? 'GET'
    const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })

    if (url === '/api/config') {
      return json({ default_model: 'qwen', default_system: 'be brief', mcp_separator: '_', context_windows: {}, default_context_window: 0 })
    }
    if (url === '/api/models') {
      this.modelFetches++
      return json({ object: 'list', data: this.models })
    }
    if (url === '/api/tools') return json({ tools: [], servers: {} })
    if (url === '/api/tools/approve') return json({})
    if (url === '/api/sessions') {
      const sessions = [...this.store.entries()].map(([id, body]) => {
        const doc = JSON.parse(body) as Conversation
        return { id, title: doc.title, createdAt: doc.createdAt, updatedAt: doc.updatedAt }
      }).sort((a, b) => b.updatedAt - a.updatedAt || (a.id < b.id ? -1 : 1))
      return json({ sessions })
    }
    if (url.startsWith('/api/sessions/')) {
      const id = decodeURIComponent(url.slice('/api/sessions/'.length))
      if (method === 'GET') {
        const body = this.store.get(id)
        return body ? new Response(body, { status: 200 }) : json({ error: 'not found' }, 404)
      }
      if (method === 'DELETE') {
        this.deletes.push(id)
        this.store.delete(id)
        return new Response(null, { status: 204 })
      }
      const body = String(init?.body)
      this.puts.push({ id, body })
      let outcome = { ok: true, status: 204 }
      if (this.holdSaves) {
        outcome = await new Promise(resolve => this.holds.push(resolve))
      }
      const failure = this.failSave?.(id, body)
      if (failure) outcome = { ok: false, status: failure }
      if (!outcome.ok) return json({ error: 'disk full' }, outcome.status)
      this.store.set(id, body)
      return new Response(null, { status: 204 })
    }
    if (url === '/api/chat') {
      const body = JSON.parse(String(init?.body)) as Record<string, unknown>
      const stream = new EventStream()
      this.chatPosts.push({ body, stream, signal: init?.signal })
      if (this.chatRejection) {
        const { status, code, message } = this.chatRejection
        return json({ error: { code, message } }, status)
      }
      init?.signal?.addEventListener('abort', () => stream.close())
      return new Response(stream.stream, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
    }
    throw new Error(`unexpected request ${method} ${url}`)
  }
}
