import type { Conversation, SessionSummary } from '../types'

// the storage seam. the app talks only to this interface, and every operation
// takes the id explicitly -- the document body never carries one.
export interface SessionStore {
  list(): Promise<SessionSummary[]>
  get(id: string): Promise<Conversation | null>
  save(id: string, doc: Conversation): Promise<void>
  remove(id: string): Promise<void>
}

// a safe filename is not necessarily a URL-safe string: 'issue#1' is a legal
// conversation name, and the '#' in a URL built by concatenation would
// truncate the request at the fragment separator, sending a different path
// and 404-ing the conversation into a ghost. the mux hands the handler the
// decoded segment, so 'issue%231' arrives as 'issue#1' and the store's id
// rule passes it unchanged.
function sessionUrl(id: string): string {
  return `/api/sessions/${encodeURIComponent(id)}`
}

// reads both documented error shapes -- a plain 'error' string and the nested
// 'error.message' object -- falling back to the status only when the body is
// unparseable or carries neither. a rejection from any operation therefore
// carries a message the error line can show.
async function failure(response: Response): Promise<Error> {
  try {
    const body = await response.json()
    if (typeof body?.error === 'string') return new Error(body.error)
    if (typeof body?.error?.message === 'string') return new Error(body.error.message)
  } catch {
    // an unparseable body falls through to the status
  }
  return new Error(response.statusText || `HTTP ${response.status}`)
}

// the backend adapter. every response is checked against its documented
// status; there is no warn-and-swallow path, so a save that never reached
// disk surfaces as a failure rather than as a success the app acts on.
export const diskSessionStore: SessionStore = {
  async list(): Promise<SessionSummary[]> {
    const response = await fetch('/api/sessions')
    if (response.status !== 200) throw await failure(response)
    const body = await response.json()
    return body?.sessions ?? []
  },

  async get(id: string): Promise<Conversation | null> {
    const response = await fetch(sessionUrl(id))
    if (response.status === 404) return null
    if (response.status !== 200) throw await failure(response)
    return await response.json()
  },

  async save(id: string, doc: Conversation): Promise<void> {
    const response = await fetch(sessionUrl(id), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(doc),
    })
    if (response.status !== 204) throw await failure(response)
  },

  async remove(id: string): Promise<void> {
    const response = await fetch(sessionUrl(id), { method: 'DELETE' })
    if (response.status !== 204) throw await failure(response)
  },
}
