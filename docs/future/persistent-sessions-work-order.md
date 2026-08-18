# persistent sessions — work order

two stages. stage 1 is backend-only and verifiable headless with curl; stage 2 rewires the frontend and synthesizes `docs/current/`. the spec is `persistent-sessions.md` in this directory; its seam census carries the settled calls. `/api/chat`, the SSE protocol, and `internal/llm` are untouched.

## critical files

| file | change |
|---|---|
| `internal/config/config.go` | `Sessions`, `DataDir` fields; default `local`; validation; `SessionDataDir()` resolution |
| `internal/session/store.go` | new package: `Store` (dir + mutex), `NewStore`, `List` / `Get` / `Save` / `Delete` / `Clear`, atomic writes, id safety |
| `internal/session/store_test.go` | round-trip, projection, invalid documents, id mismatch, path safety, delete/clear, concurrency |
| `internal/api/sessions.go` | new: the five handlers; local-mode 503 |
| `internal/api/sessions_test.go` | httptest: disk-mode CRUD; local-mode 503; validation rejections |
| `internal/api/api.go` | `API` gains `sessions *session.Store` (nil in local mode); routes; `/api/config` response gains `sessions` |
| `cmd/pane/main.go` | fail-fast store construction in disk mode; pass the store to `api.NewAPI` |
| `cmd/pane/new.go`, `pane.yaml.example` | commented `sessions` / `data_dir` lines |
| `ui/src/types.ts` | `ConfigResponse.sessions`; `SessionSummary` |
| `ui/src/lib/sessionStore.ts` | new: `SessionStore` interface + `localSessionStore` + `diskSessionStore` |
| `ui/src/hooks/useSessions.ts` | new: mode-gated summaries state + store operations |
| `ui/src/hooks/useConfig.ts` | pass `sessions` through; `emptyConfig` carries `sessions: ''` |
| `ui/src/App.tsx` | conversations move from `useLocalStorage` to `useSessions`; `activeDoc` state; commit / delete / clear through the store |
| `docs/current/pane.md` | endpoints, `/api/config` schema, config keys, principle 5 reworded, deferred item 6 scoped, frontend section |

## stage 1 — backend store + api

### config — `internal/config/config.go`

- `Sessions string` — `local` (compiled default) or `disk`. validation rejects any other value, naming it: `sessions must be 'local' or 'disk', got '...'`.
- `DataDir string` — optional; meaningful only in disk mode.
- `func (c *Config) SessionDataDir() (string, error)` — returns the resolved data directory: `DataDir` when set, else `$XDG_DATA_HOME/pane` when `XDG_DATA_HOME` is non-empty, else `~/.local/share/pane`. mirrors the XDG handling in `globalConfigPath`.
- `pane.yaml.example` and the `pane new` template gain two commented lines near the listen block: `#sessions: disk` (one-line comment: store conversations on disk instead of browser localStorage) and `#data_dir: ~/.local/share/pane` (comment: disk mode only; default shown).

### store — `internal/session/store.go`

`type Store struct { dir string; mu sync.Mutex }` where `dir` is the sessions directory.

- `NewStore(dir string) (*Store, error)` — `os.MkdirAll(dir, 0o755)`. called once at startup in disk mode; a failure here is fatal (see main).
- `safeID(id string) (string, error)` — rejects empty, `.` and `..`, anything containing a path separator, and anything where `filepath.Clean(id) != id`. the final guard: `filepath.Join(dir, id)` must have `dir` as its cleaned parent. this is defense against a URL segment the mux already keeps slash-free; the store does not trust it.
- `List() ([]Summary, error)` — reads every `*.json` in `dir`. `Summary` is a small struct (`ID`, `Title string`; `CreatedAt`, `UpdatedAt int64`) filled by unmarshaling just the projection fields (encoding/json's case-insensitive matching handles the document's camelCase keys). files that fail to read or parse are skipped with `dl.Warnf`. sorted by `UpdatedAt` descending, `ID` as the tiebreak.
- `Get(id string) ([]byte, error)` — `safeID`, then read. returns the stored bytes only if they are a JSON object (valid JSON whose first non-whitespace byte is `{`); otherwise an error naming the id.
- `Save(id string, doc []byte) error` — `safeID`; reject bodies over 32 MiB; reject anything that is not a JSON object; unmarshal only `{ID string}` and require `ID == id`. then write atomically: temp file in `dir` (`os.CreateTemp`), write, `Chmod 0o644`, rename over the target. mutex around the read-then-write of nothing — Save is a single write, but the mutex still guards every file operation so List never observes a half-written name (rename is atomic, the mutex keeps Clear consistent).
- `Delete(id string) (bool, error)` — `safeID`, remove; reports `false` when the file was absent, no error for it.
- `Clear() error` — removes every `*.json` directly in `dir`. not recursive; never touches the directory itself or anything outside it.
- all file operations under `mu`.

### api — `internal/api/sessions.go` + `api.go`

- `API` struct gains `sessions *session.Store` — nil in local mode. `NewAPI` takes it.
- routes: `GET /api/sessions`, `GET /api/sessions/{id}`, `PUT /api/sessions/{id}`, `DELETE /api/sessions/{id}`, `DELETE /api/sessions`.
- local mode (`a.sessions == nil`): all five return `503` with `{"error": {"code": "sessions_disabled", "message": "session store not enabled; set sessions: disk in config"}}`.
- `handleListSessions` — `store.List()`, respond `{"sessions": [...]}` through `dd.UnbindJSONWriter`; projection fields use explicit dd name overrides to the document's camelCase (`createdAt`, `updatedAt`). an empty store returns `{"sessions": []}`, never null.
- `handleGetSession` — missing: `404 {"error": "session 'id' not found"}`. stored but unparseable: `500 {"error": "session 'id' is not a valid document"}`. otherwise the raw stored bytes with `Content-Type: application/json`.
- `handleSaveSession` — `io.ReadAll` the body; `store.Save`; validation failures are `400 {"error": "..."}` with the store's reason, size overrun `413`. success `204`.
- `handleDeleteSession` — absent: `404`; otherwise remove, `204`.
- `handleClearSessions` — `store.Clear()`, `204`.
- `configResponse` gains `Sessions string`; `handleConfig` fills it from `cfg.Sessions`. always present, never omitempty — it is the frontend's mode gate.

### main — `cmd/pane/main.go`

after config load, before the server starts:

```go
var store *session.Store
if cfg.Sessions == "disk" {
    dir, err := cfg.SessionDataDir()
    // on err: dl.Fatalf("session store: %v", err)
    store, err = session.NewStore(filepath.Join(dir, "sessions"))
    // on err: dl.Fatalf("session store: %v", err)
    dl.Infof("session store: %s", storeDir)
}
```

`api.NewAPI` receives the store (nil in local mode).

### stage 1 verification

- `make test` (go test + vet) green.
- headless pass with a temp config: `sessions: disk`, `data_dir: <tmp>`, a dummy endpoint, `listen: 127.0.0.1:18400`.
  - `GET /api/sessions` → `{"sessions": []}`; `/api/config` reports `"sessions": "disk"`.
  - `PUT /api/sessions/abc` with a full conversation document → 204; `GET /api/sessions/abc` returns the bytes unchanged; the list shows its projection sorted by updated.
  - `PUT` with a body whose `id` differs from the path → 400; with invalid JSON → 400; path via `GET /api/sessions/..` → not a route (mux), and a directly-constructed store call with `..` fails `safeID`.
  - `DELETE /api/sessions/abc` → 204; again → 404; `DELETE /api/sessions` → 204 and the directory is empty.
  - two `PUT`s of different sizes to the same id: the file is always a complete document (atomic rename).
- a local-mode instance: all five endpoints → 503 with the `sessions_disabled` code; `/api/config` reports `"sessions": "local"`; no data directory is created.
- an invalid `sessions: blob` value and a non-writable `data_dir` each fail startup with the clear error.

## stage 2 — frontend rewiring + docs

### types + config

- `ConfigResponse` gains `sessions: string` (`''` in `emptyConfig` until the fetch lands). `useConfig` passes `sessions: data.sessions || ''` through.
- `types.ts` gains `SessionSummary { id: string; title: string; createdAt: number; updatedAt: number }`.

### adapters — `ui/src/lib/sessionStore.ts`

the interface from the spec. two implementations:

- `localSessionStore` — the `pane:conversations` key. `list` parses the stored array (defaulting to `[]` on any parse failure, matching today's `useLocalStorage` posture) and projects, sorted by updated descending. `get` finds by id. `save` upserts by id into the stored array (insert when absent, replace when present) and writes it back. `remove` filters by id. `clear` deletes the key. every operation is synchronous work wrapped in the async signatures, so the app code is one path for both modes.
- `diskSessionStore` — `list` fetches `GET /api/sessions`; `get` fetches the document (`404` resolves to `null`); `save` PUTs the document; `remove` DELETEs the id; `clear` DELETEs the collection. fetch failures `console.warn` and resolve empty/ignored — the same posture as today's localStorage try/catch. the backend serves the UI, so in practice these only fail when the binary is dying.

### hook — `ui/src/hooks/useSessions.ts`

`useSessions(mode: 'local' | 'disk' | null)`:

- the store instance is chosen from `mode` (`null` → inert: no storage access of any kind).
- `summaries` state, loaded once when `mode` first becomes non-null; `loading` state until then.
- exposes `{ summaries, loading, get, save, remove, clear }`. `save` / `remove` / `clear` call the store and then update `summaries` locally (upsert projection / filter / empty) so the rail moves without a refetch.
- projection of a saved document: `{ id, title, createdAt, updatedAt }` from the document itself — both modes sort by updated descending, so the rail behaves identically across modes.

### App rewiring — `ui/src/App.tsx`

- `conversations` / `setConversations` (useLocalStorage) are replaced by `const sessions = useSessions(config.sessions === 'disk' ? 'disk' : config.sessions === 'local' ? 'local' : null)`.
- new state: `activeDoc: Conversation | null` — the full document for the active conversation. it is `null` whenever `activeId` is, and set whenever `activeId` is.
- the conversation-sync effect (on `activeId`) seeds `chat.loadConversation(activeDoc)` — which already seeds the usage record, so the meter's stored reading rides along.
- `handleNewConversation`: build an empty document (`nanoid` id, empty title/messages, current timestamps), `sessions.save(doc)`, set `activeDoc` + `activeId`.
- the commit effect (on `chat.messages`, `chat.usageRecord`): when the owner matches `activeId` and messages are non-empty, build the document from `activeDoc` (messages replaced, `usage` from `chat.usageRecord`, title falling back to `extractTitle`, `updatedAt: Date.now()`), set it as `activeDoc`, and `sessions.save` it.
- `handleSend`'s no-active-conversation branch creates and saves its document before sending, exactly as it creates its object today.
- `handleDeleteConversation`: `sessions.remove(id)`; when deleting the active one, also null `activeDoc` / `activeId` and clear chat messages.
- `handleClearAllData`: after the confirm, `sessions.clear()` (in local mode this deletes the `pane:conversations` key), then remove `pane:activeConversation` and `pane:chatPreferences`, then reload. full-wipe semantics unchanged in both modes.
- `ConversationList` receives `summaries` (its title fallback for empty titles stays where it is).
- `canExportActiveConversation` and export read from `activeDoc` + `chat.messages` instead of the found array entry.

`useChat` is untouched: `loadConversation` already seeds `usageRecord` from the conversation object (verified against the current code).

### docs — `docs/current/pane.md`

- the HTTP API table gains the five `/api/sessions` rows; a schema subsection documents the list shape, the document round-trip, the error table (503 local mode, 400 validation, 404, 413, 500 corrupt file).
- the `/api/config` schema gains `sessions`.
- the configuration section documents `sessions` and `data_dir` with the XDG default.
- principle 5 is reworded: stateless by default — in the default `local` mode conversations live in the browser and the backend is a pure proxy; in `disk` mode the backend owns the record, one opaque JSON file per conversation, while `/api/chat` stays full-history-per-request and keeps no in-memory state.
- the frontend section documents the adapter interface, the mode gate from `/api/config`, what stays in per-browser localStorage (active conversation, preferences), the shared rail ordering (updated descending), and the usage record's round trip through the store.
- deferred item 6 (localStorage quota) is scoped to local mode.
- "what pane is not" and the build/run sections gain a line: disk mode creates its data directory at startup.

### stage 2 verification

- `make build` (frontend + binary) and `make test` green.
- the manual pass below, in a fresh profile, disk mode.

### manual pass (operator)

disk mode (`sessions: disk` in config, fresh browser profile):

1. start a conversation, watch a tool call run, let the meter land a reading. reload. the conversation, tool blocks, collapsed thinking, and the meter's bands are all back.
2. the data directory holds one JSON file per conversation; its bytes are the conversation object itself.
3. a second browser (different profile) pointed at the same pane instance sees the same rail.
4. reply to an older conversation — it moves to the top of the rail.
5. delete one conversation; it is gone from rail and disk. clear all: rail empty, directory empty, preferences and active-tab state reset.
6. kill the pane mid-stream, reload: committed history is intact, the in-flight turn is lost (same as today).
7. `/api/sessions` never appears in the network panel.

local mode (default config, existing profile):

1. behavior is as before — rail, meter, tool blocks, export — with the one visible change: the rail now orders by last activity.
2. no `/api/sessions` request is ever made; curling one against this instance returns the 503.
3. existing localStorage conversations are untouched and keep working.

config edges:

- `sessions: blob` → startup failure naming the value.
- `sessions: disk` with a non-writable `data_dir` → startup failure naming the directory.

## closeout

- the operator flips the card; both `docs/future/` documents are removed, their value now in `docs/current/pane.md` and the code.
- changelog: `FEATURE` — disk-backed session storage (`sessions: disk`); `CHANGE` — conversation rail orders by last activity; `/api/config` reports the session mode.
- accepted residuals (last-write-wins, out-of-band corruption, no auth in disk mode, local-mode quota) are carried in `docs/current/` — the error-handling tables and the deferred list — not re-synthesized into new future documents.
- mercurius calibration: `review_focus` returns to its no-arc-in-flight note; this arc's settled guards are retired with it, the cross-arc guards (thinking trio, usage trio, observability) carry forward.