# persistent sessions — spec

the record of the work should outlive the tab. this arc moves conversation storage from the browser onto disk behind the backend — as an opt-in config mode, with localStorage retained as the first-class default.

## problem

the founding principle — stateless backend, conversations in the browser — fits the appliance story badly in one specific way: the record of the work does not outlive the tab. a browser profile wipe, a reimage, a switch of browsers, and every conversation is gone. markdown export saves what the reader thinks to save in the moment; the rest is lost.

the context meter made the asymmetry concrete (2026-08-17): the per-conversation usage record is self-contained on the conversation object, and the design session noted that when storage leaves the browser the record moves with the conversation — it serializes with the history it describes and needs no re-derivation. this arc is that revisit.

deferred item 6 in `docs/current/pane.md` (no graceful behavior when localStorage fills) is resolved by construction in disk mode: a disk store has no practical quota. in local mode the item remains, scoped to that mode.

## shape

storage ownership moves to the backend when the operator opts in. the browser cannot write to disk; only the backend can. the protocol does not move: `/api/chat` still receives the full history on every request, the backend keeps no in-memory conversation state, and the SSE stream is unchanged. what moves is where the record lives, not how it flows.

two deployment modes, selected by one config key:

| mode | record | selection |
|---|---|---|
| `local` (default) | browser localStorage, as it is today | compiled default; existing deployments are untouched |
| `disk` | one JSON file per conversation under the data directory | `sessions: disk` in config |

the mode is a deployment property, not a UI property. the backend announces it through `/api/config`; the frontend picks a storage adapter; the UI is identical in both modes. there is no toggle in the interface, because switching modes is an operator act, not a reader act.

one visible refinement lands in both modes: the conversation rail sorts by last activity (updated, descending) instead of insertion order. replying to an older conversation brings it to the top — standard chat-client behavior, and the ordering a persistent record wants, since the rail's job becomes "where was i".

### the store

`internal/session` is a new backend package: a file-backed document store.

- **one file per conversation.** `<data_dir>/sessions/<id>.json`, where the data directory resolves from `data_dir` in config, defaulting to `~/.local/share/pane` (XDG_DATA_HOME-aware, mirroring the existing config-path resolution). the `sessions/` subdirectory leaves room for future disk data in the same home.
- **opaque documents.** the file is exactly the camelCase shape the frontend already speaks (`id`, `title`, `messages`, `createdAt`, `updatedAt`, `usage`). the backend never interprets messages — it validates only that the body is a JSON object whose top-level `id` matches the path. the canonical shape is end to end; there is no wire translation in either direction.
- **projection for the list.** the rail needs only `id`, `title`, `createdAt`, `updatedAt`. the list reads that projection from each file, sorted by updated descending. a file whose projection will not parse is skipped with a logged warning — one bad file never breaks the rail. the projection uses the document's camelCase fields: it is a subset of the opaque document, and one case per resource.
- **atomic writes.** temp file in the same directory, then rename. a per-store mutex serializes operations — one binary, one process, no locking protocol.
- **fail fast.** in disk mode, a store that cannot be created fails startup with a clear error. an operator who opted into persistence is told when it did not take; it never silently downgrades to a browser that will lose the record.

### api

additions only. every existing endpoint is unchanged.

| endpoint | method | description |
|---|---|---|
| `/api/sessions` | GET | list: projections of every stored conversation, sorted by updated descending. `{"sessions": [{id, title, createdAt, updatedAt}]}` |
| `/api/sessions/{id}` | GET | the full session document, as stored |
| `/api/sessions/{id}` | PUT | upsert the full document. create-or-replace; the frontend owns id generation, as it does today |
| `/api/sessions/{id}` | DELETE | delete one conversation |
| `/api/sessions` | DELETE | delete every stored conversation (the clear-all action's new home) |

error behavior:

- **local mode.** all five return `503` with `{"error": {"code": "sessions_disabled", "message": "session store not enabled; set sessions: disk in config"}}`. the mode is discoverable through `/api/config`, which is the frontend's only gate — it never calls these endpoints in local mode.
- **put validation.** a body that is not a JSON object, whose top-level `id` does not match the path, or whose path is not a safe single file name, is rejected with `400`. a body above a 32 MiB cap is `413`.
- **get of a stored file that no longer parses** (corrupted out of band) returns `500` with an error naming the id.
- **delete of a missing id** is `404`.

`/api/config` gains one field: `sessions`, always present, `local` or `disk`.

### frontend

a storage adapter sits behind one interface, and the app talks only to the interface:

```typescript
interface SessionStore {
  list(): Promise<SessionSummary[]>
  get(id: string): Promise<Conversation | null>
  save(conversation: Conversation): Promise<void>
  remove(id: string): Promise<void>
  clear(): Promise<void>
}
```

- **`localSessionStore`** wraps today's `pane:conversations` key with the same shape and the same write triggers. existing localStorage data in local-mode deployments keeps working with no migration — the adapter reads the bytes pane already wrote.
- **`diskSessionStore`** fetches against `/api/sessions*`.
- **`useSessions(mode)`** owns the summaries state (the rail's source of truth) and exposes the store's operations. `mode` is `null` until `/api/config` lands; the hook is inert until then, so neither mode touches storage before the mode is known.

the conversation surface of `App.tsx` rewires from `useLocalStorage('pane:conversations')` to the hook:

- on mode-ready, load the summaries; on selection, fetch the full document and seed the chat hook. `loadConversation` already seeds the usage record from the conversation object, so the meter's reading moves with the conversation and shows the same bands after a reload as before it — the revisit condition the usage arc named.
- commit triggers — message or usage change, new conversation, title — build the full document and `save` it. these are the same triggers that write localStorage today; the write path is unchanged, the destination is not.
- delete and clear-all go through `remove` / `clear`. clear-all additionally drops pane's per-browser keys and reloads, keeping today's full-wipe semantics in both modes.

**what stays in the browser, in both modes:** `pane:activeConversation` and `pane:chatPreferences`. these are per-browser view state — a second machine pointed at the same store should restore the sessions, not necessarily the last-open tab or the last-dialed model.

**no migration.** switching a deployment from local to disk starts at an empty store. the localStorage copy stays in the browser — readable, exportable as markdown — until the user clears it. nothing is imported, nothing is destroyed, no first-run prompt.

## seam census

the calls made in the design session, confirmed by the operator 2026-08-18:

1. **storage ownership / protocol.** the backend owns storage in disk mode. `/api/chat` keeps taking the full history on every request and the backend keeps no in-memory conversation state. the alternative — the backend loads history from the store and `/api/chat` takes an id — makes the backend stateful per session and buys nothing for a one-human, one-instance appliance.
2. **storage format.** one JSON file per conversation, opaque to the backend, the frontend's camelCase shape canonical end to end. a single store file rewrites the whole estate on every change; SQLite violates the no-database principle.
3. **no migration.** localStorage data is never imported into the disk store. it stays in the browser; a reader who wants it on disk exports it as markdown first.
4. **localStorage is first-class.** `sessions: local` is the compiled default and remains a fully supported mode for deployments that want to stay quick and stateless. disk is an opt-in config setting, not a replacement.
5. **browser state stays local.** active-conversation selection and chat preferences remain per-browser localStorage state in both modes.
6. **data directory.** `data_dir` in config, default `~/.local/share/pane`, configurable. the operator confirmed the default and the configurability.
7. **projection case.** the list projection uses the document's camelCase fields rather than the snake_case the rest of pane's API speaks, on the ground that the projection is a subset of the opaque document. agent-recommended, recorded here for the operator to reject; the review may re-raise it.

## scenarios

**s1 — the appliance.** a machine runs pane in disk mode beside ollama. the browser profile dies, the OS is reimaged, the user moves to a different browser. every conversation — tool-call history, thinking blocks, usage record — is present on first load. the context meter shows the same bands after the reimage as before it.

**s2 — the stateless container.** pane runs in an ephemeral container. config stays at the default: sessions live in the container's browser localStorage and die with the container; no directory is ever created on any volume. the binary is byte-identical to a disk-mode build — the mode is data, not code.

**s3 — the switch.** a deployment moves from local to disk. the disk store starts empty; the old conversations remain in the browser's localStorage, readable and exportable until the user clears browser data. no import, no prompt, no data loss.

**s4 — the two tabs.** two tabs of the same disk-mode pane hold independent in-memory state. if both edit the same conversation, the last PUT wins and the other tab is stale until it reloads. this is the same last-write-wins property the in-memory state has today, carried onto disk; the accepted residual below names it.

## what does not change

- the SSE protocol, the chat loop, tool calls, approvals, thinking display — untouched.
- the `/api/chat` request shape: full history, system-prompt mode. the backend remains a stateless proxy.
- local mode: same keys, same shapes, same write triggers. the only visible change is the rail ordering both modes now share. the new endpoints exist and return 503; the frontend never calls them.
- no auth, no settings UI, no new UI surface. the mode is config-only.
- the usage meter: record placement, bands, and invalidation rules are unchanged. in disk mode the record now round-trips through the store.

## accepted residuals

- **last-write-wins.** no cross-tab or cross-process conflict resolution. two writers to one conversation: the last PUT wins, the other writer's in-memory state is stale until reload. acceptable for a one-human appliance; a real sync protocol is deferred.
- **corruption is out of band.** the store validates on write. a file damaged after the fact (disk error, manual edit) is skipped in the list with a warning and fails on get with an error naming the id. there is no repair path.
- **disk mode has no auth.** the sessions sit behind whatever `listen` address is configured. binding to a non-loopback address exposes full conversation history to the network — that is the authentication-and-accounts card's territory, not this arc's.
- **local-mode quota.** the deferred item remains, scoped: a disk store has no practical quota, but a local-mode browser profile that overfills localStorage still has no graceful behavior.

## deferred (and why)

- **import into the disk store** (localStorage → disk). the operator settled no migration. a manual path — markdown export, then an import surface — is a feature to be asked for, not a repair this arc owes.
- **retention / pruning.** the store grows without bound. a policy (age, count, size) is a separate call with separate numbers.
- **carrying the data directory between machines.** a tarball of the data directory is already portable; first-class sync or backup is a different arc with different stakes.
- **auth on the session endpoints.** the authentication-and-accounts card owns it.
- **search, pinning, folders across conversations.** rail ergonomics, not storage.
- **a non-JSON store format** (SQLite, single-file store). declined on the no-database principle; the format earns its way in if file counts ever stress the filesystem.