import { useCallback, useEffect, useRef, useState } from 'react'
import { diskSessionStore } from '../lib/sessionStore'
import type { StoredConversation } from '../types'

type Updater = StoredConversation[] | ((prev: StoredConversation[]) => StoredConversation[])

function describe(reason: unknown): string {
  return reason instanceof Error ? reason.message : String(reason)
}

// the mirror hook, shaped like useLocalStorage so the app code around it keeps
// its form: state is the working truth for the tab's lifetime and the store is
// its durable mirror, hydrated on load and persisted on every change.
export function useSessions() {
  const [conversations, setConversations] = useState<StoredConversation[]>([])
  // the mirror reads the ref, renders read the state; the setter keeps the two
  // equal. the ref is what makes the diff available at call time.
  const copyRef = useRef<StoredConversation[]>([])
  // 'loading' means the working copy is not yet installed, not that a fetch
  // is in flight: a failed list settles nothing, so the flag stays true and
  // the session gate stays closed over an estate the tab could not read. one
  // boundary, one meaning.
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  // one serial chain in invocation order: a remove issued after a save cannot
  // execute ahead of it and re-create the file. this is the adapter's entire
  // ordering machinery.
  const chainRef = useRef<Promise<void>>(Promise.resolve())

  const enqueue = useCallback((label: string, operation: () => Promise<void>): Promise<void> => {
    const settled = chainRef.current.then(async () => {
      try {
        await operation()
        setError(null)
      } catch (reason) {
        setError(`${label}: ${describe(reason)}`)
      }
    })
    chainRef.current = settled
    return settled
  }, [])

  // hydration: the list, then a get for each id it named, installed in the
  // order the list returned them (updated descending, id ordinal-ascending --
  // the store's comparator), not in get-resolution order.
  useEffect(() => {
    let cancelled = false

    void (async () => {
      let summaries
      try {
        summaries = await diskSessionStore.list()
      } catch (reason) {
        // a failed list settles nothing: loading stays true, the copy stays
        // empty, and the session gate stays closed over an estate the tab
        // could not read. the recovery is a reload after the backend recovers.
        if (!cancelled) setError(`loading conversations: ${describe(reason)}`)
        return
      }

      const failures: string[] = []
      const fetched = await Promise.all(summaries.map(async summary => {
        try {
          const doc = await diskSessionStore.get(summary.id)
          return doc ? { id: summary.id, doc } : null
        } catch (reason) {
          // one unreadable file drops its entry and still settles the rest.
          failures.push(`loading conversation '${summary.id}': ${describe(reason)}`)
          return null
        }
      }))

      // react repeats effect setup under StrictMode in development; without
      // this flag a superseded instance's late results could install a stale
      // snapshot over a local change the surviving pipeline made. production
      // mounts once, so the flag is a no-op there.
      if (cancelled) return

      const copy = fetched.filter((entry): entry is StoredConversation => entry !== null)
      copyRef.current = copy
      setConversations(copy)
      setError(failures.length > 1
        ? `${failures[0]} (and ${failures.length - 1} more)`
        : failures[0] ?? null)
      // the copy is fully installed and loading flips in the same step: one
      // boundary, one meaning.
      setLoading(false)
    })()

    return () => { cancelled = true }
  }, [])

  // the update function, with the same call shape useLocalStorage returns. the
  // mirror runs exactly once per call, here at call time: never inside react's
  // updater (react may invoke updaters more than once, so I/O there fires
  // speculatively or twice) and never in a later effect (which settles after
  // the state it mirrors has logically moved, and could join the chain behind
  // a logically earlier operation).
  const mirror = useCallback((value: Updater) => {
    const previous = copyRef.current
    const next = typeof value === 'function' ? value(previous) : value

    copyRef.current = next
    setConversations(next)

    const before = new Map(previous.map(entry => [entry.id, entry.doc]))
    for (const entry of next) {
      // the app's immutable updates build new objects only where they changed,
      // so a reference-unchanged document is not saved. removals are not
      // diffed -- the destructive handler calls remove explicitly, because it
      // awaits the settle to release its guard.
      if (before.get(entry.id) === entry.doc) continue
      void enqueue(`saving conversation '${entry.id}'`, () => diskSessionStore.save(entry.id, entry.doc))
    }
  }, [enqueue])

  const remove = useCallback((id: string): Promise<void> => {
    return enqueue(`deleting conversation '${id}'`, () => diskSessionStore.remove(id))
  }, [enqueue])

  return { conversations, setConversations: mirror, loading, error, remove }
}
