import { useState, useCallback, useEffect, useLayoutEffect, useRef } from 'react'
import { nanoid } from 'nanoid'
import { useLocalStorage } from './hooks/useLocalStorage'
import { useSessions } from './hooks/useSessions'
import { useConfig } from './hooks/useConfig'
import { useModels } from './hooks/useModels'
import { useTools } from './hooks/useTools'
import { useChat } from './hooks/useChat'
import { ChatView } from './components/ChatView'
import { ConversationList } from './components/ConversationList'
import { Toolbar } from './components/Toolbar'
import { ToolPanel } from './components/ToolPanel'
import { RecoveryPanel } from './components/RecoveryPanel'
import {
  buildConversationMarkdownFilename,
  conversationToMarkdown,
  downloadMarkdown,
  hasExportableMessages,
} from './lib/exportMarkdown'
import {
  assessRecovery,
  buildChatRequest,
  MAX_BODY_BYTES,
  prepareTurn,
  utf8ByteLength,
  validateRecovery,
  type CallDecision,
} from './lib/turnRecord'
import type { Conversation, ChatPreferences, Execution, Message, SystemPromptMode, TurnRecord, UsageRecord } from './types'

const defaultChatPreferences: ChatPreferences = {
  modelOverride: null,
  systemPromptMode: 'default',
  systemPromptCustom: '',
}

// the references the last mirrored document for the active conversation
// holds. while chat's state is still these references, a commit is a read,
// not a write.
interface CommitSnapshot {
  messages: Message[]
  usage: UsageRecord | null
  turns: TurnRecord[] | undefined
}

export default function App() {
  const {
    conversations,
    setConversations,
    loading: sessionsLoading,
    error: sessionsError,
    remove: removeConversation,
    saveCandidate,
    install: installConversation,
  } = useSessions()
  const [activeId, setActiveId] = useLocalStorage<string | null>('pane:activeConversation', null)
  const [preferences, setPreferences] = useLocalStorage<ChatPreferences>('pane:chatPreferences', defaultChatPreferences)
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [toolPanelOpen, setToolPanelOpen] = useState(false)
  const chatOwnerIdRef = useRef<string | null>(null)
  const skipNextConversationSyncRef = useRef(false)
  const savedSnapshotRef = useRef<CommitSnapshot | null>(null)
  // reactive state closes the component-level gate (the buttons re-render
  // disabled); the mirror ref is what the handlers read, since state read
  // inside the same event closure is stale by one render.
  const [destructivePending, setDestructivePending] = useState(false)
  const destructivePendingRef = useRef(false)
  // the preparation guard: held from building a turn's candidate until its
  // save-and-send decision finishes. the ref is what handlers read; the
  // state re-renders the controls locked.
  const [preparing, setPreparing] = useState(false)
  const preparingRef = useRef(false)
  const [sendError, setSendError] = useState<string | null>(null)
  const mountedRef = useRef(true)
  const activeIdRef = useRef<string | null>(null)

  const { config, error: configError } = useConfig()
  const selectedModel = preferences.modelOverride || config.default_model
  const { models, refresh: refreshModels } = useModels(selectedModel)
  const { tools, servers } = useTools()
  const chat = useChat()

  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false }
  }, [])

  useEffect(() => {
    activeIdRef.current = activeId
  }, [activeId])

  // request completion refreshes availability: a failure may have changed
  // what the listing reports.
  const wasStreamingRef = useRef(false)
  useEffect(() => {
    if (wasStreamingRef.current && !chat.isStreaming) refreshModels()
    wasStreamingRef.current = chat.isStreaming
  }, [chat.isStreaming, refreshModels])

  useEffect(() => {
    if (localStorage.getItem('pane:chatPreferences')) return

    const migrated = migrateLegacyPreferences()
    if (migrated) {
      setPreferences(migrated)
    }
  }, [setPreferences])

  const activeConversation = conversations.find(c => c.id === activeId)
  const canExportActiveConversation = activeConversation
    ? hasExportableMessages({ ...activeConversation.doc, messages: chat.messages })
    : false
  // the session gate: every session-mutating entry point is closed while
  // hydration is incomplete or a destructive operation is pending. loading
  // moves only true->false, so state suffices for that half.
  const canNavigate = !sessionsLoading && !destructivePending && !preparing
  const selectedInfo = models.find(model => model.id === selectedModel)
  const authBlocked = selectedInfo?.auth_state === 'login_required' || selectedInfo?.auth_state === 'error'
  const recovery = assessRecovery(chat.turns, chat.activeTurnId)
  const canSend = canNavigate && !authBlocked && recovery.kind !== 'reconcile'
  const sendNotice = selectedInfo?.auth_state === 'login_required'
    ? `model '${selectedModel}' needs a subscription login: run 'pane auth login openai', then this page updates on its own`
    : selectedInfo?.auth_state === 'error'
      ? `model '${selectedModel}' credentials are unusable: ${selectedInfo.last_error?.message ?? 'check pane auth status openai'}`
      : recovery.kind === 'reconcile' ? 'the last turn was interrupted: record its outcome below before sending' : null
  // the session error takes the line when present, so a persistent config
  // failure can never mask a live one.
  const appError = sendError ?? sessionsError ?? configError

  const setDestructive = useCallback((pending: boolean) => {
    destructivePendingRef.current = pending
    setDestructivePending(pending)
  }, [])

  // sync chat state when the selection changes, and at the
  // hydration-completion transition. a layout effect rather than a passive
  // one: react flushes it before paint, so no painted frame shows the
  // composer enabled while the chat area, meter, or snapshot still hold
  // another conversation's state.
  useLayoutEffect(() => {
    // during hydration the rail is empty and the gate is up, so an empty find
    // of a retained id means nothing yet -- skip rather than seed from it.
    if (sessionsLoading) return

    if (skipNextConversationSyncRef.current) {
      skipNextConversationSyncRef.current = false
      chatOwnerIdRef.current = activeId
      savedSnapshotRef.current = null
      return
    }

    // the ghost case: a retained id naming no stored conversation. clearing
    // the selection re-runs this effect on the null branch, which resets chat
    // state, the commit owner, and the meter together -- so the ghost is
    // never left as the commit owner and a send takes the create branch.
    if (activeId && !activeConversation) {
      setActiveId(null)
      return
    }

    chatOwnerIdRef.current = activeId
    savedSnapshotRef.current = activeConversation
      ? { messages: activeConversation.doc.messages, usage: activeConversation.doc.usage ?? null, turns: activeConversation.doc.turns }
      : null
    chat.loadConversation(activeConversation ? activeConversation.doc : null)
  }, [activeId, sessionsLoading]) // eslint-disable-line react-hooks/exhaustive-deps

  // mirror chat state back to the conversation when it changes. a passive
  // effect: it writes, it does not paint.
  useEffect(() => {
    if (chatOwnerIdRef.current !== activeId) return
    if (!activeId || chat.messages.length === 0) return
    // a render whose chat state was replaced before this effect ran -- a
    // selection loaded another conversation in the layout effect -- holds
    // the previous owner's record; mirroring it would write one
    // conversation's history into another.
    if (chat.messages !== chat.recordRef.current.messages || chat.turns !== chat.recordRef.current.turns) return

    // the read-is-not-a-write guard: while chat holds the same references the
    // last mirrored document holds, selecting or reloading a conversation
    // emits no PUT, so the stored updatedAt and the rail's ordering are
    // untouched by a mere open.
    // turn records are part of the snapshot: a recovery-only change -- an
    // observed tool outcome, an interruption, a reconciliation -- is a
    // document change even when messages and usage keep their references.
    const snapshot = savedSnapshotRef.current
    const turns = chat.turns.length > 0 ? chat.turns : snapshot?.turns
    if (snapshot && snapshot.messages === chat.messages && snapshot.usage === chat.usageRecord && snapshot.turns === turns) return

    const messages = chat.messages
    const usage = chat.usageRecord
    savedSnapshotRef.current = { messages, usage, turns }
    setConversations(prev => prev.map(c => {
      if (c.id !== activeId) return c
      const doc: Conversation = {
        ...c.doc,
        messages,
        usage,
        title: c.doc.title || extractTitle(messages),
        updatedAt: Date.now(),
      }
      if (turns) doc.turns = turns
      return { ...c, doc }
    }))
  }, [chat.messages, chat.usageRecord, chat.turns]) // eslint-disable-line react-hooks/exhaustive-deps

  // stopping a turn to navigate away keeps its interruption: the record is
  // written to the conversation that owns the turn, not to whichever one is
  // selected next.
  const stopActiveTurn = useCallback(() => {
    const owner = chatOwnerIdRef.current
    const interrupted = chat.abort()
    if (!interrupted || !owner) return
    savedSnapshotRef.current = { messages: interrupted.messages, usage: chat.usageRecord, turns: interrupted.turns }
    setConversations(prev => prev.map(c => c.id !== owner ? c : {
      ...c,
      doc: { ...c.doc, messages: interrupted.messages, turns: interrupted.turns, updatedAt: Date.now() },
    }))
  }, [chat, setConversations])

  const handleNewConversation = useCallback(() => {
    if (sessionsLoading || destructivePendingRef.current || preparingRef.current) return
    stopActiveTurn()

    const id = nanoid()
    const now = Date.now()
    const doc: Conversation = {
      title: '',
      messages: [],
      createdAt: now,
      updatedAt: now,
    }
    setConversations(prev => [{ id, doc }, ...prev])
    setActiveId(id)
  }, [sessionsLoading, setConversations, setActiveId, stopActiveTurn])

  const handleSelectConversation = useCallback((id: string) => {
    // selecting during the destructive window would re-attach commit
    // ownership through the sync effect, and a message mutation on the newly
    // selected conversation would then commit a save behind the in-flight
    // delete, re-creating the file after the deletion with no error.
    if (destructivePendingRef.current || preparingRef.current) return
    if (id === activeId) return
    stopActiveTurn()
    setActiveId(id)
  }, [activeId, stopActiveTurn, setActiveId])

  const handleDeleteConversation = useCallback((id: string) => {
    if (destructivePendingRef.current || preparingRef.current) return

    const deletingActive = activeId === id
    if (deletingActive) {
      setDestructive(true)
      chat.abort()
      chatOwnerIdRef.current = null
    }

    // state-first, like the localStorage it replaces: the screen shows what
    // state holds immediately. if the remove fails the error line names it,
    // the disk still holds the conversation, and a reload restores it.
    setConversations(prev => prev.filter(c => c.id !== id))

    if (deletingActive) {
      setActiveId(null)
      void removeConversation(id).then(() => setDestructive(false))
    } else {
      // a different id cannot undo the operation, so no guard is needed.
      void removeConversation(id)
    }
  }, [activeId, setConversations, setActiveId, setDestructive, removeConversation, chat])

  const handleRenameConversation = useCallback((id: string, title: string) => {
    // a rename enqueues a save; behind an in-flight delete of the same id it
    // would re-create the file the delete just removed, the hazard the
    // destructive fence exists for.
    if (destructivePendingRef.current || preparingRef.current) return
    // only the title moves: messages and usage keep their references, so the
    // read-is-not-a-write snapshot still holds, and updatedAt is untouched —
    // a rename is not activity, and the row keeps its place in the rail.
    setConversations(prev => prev.map(c => c.id === id ? { ...c, doc: { ...c.doc, title } } : c))
  }, [setConversations])

  // one selected model owns the whole turn: selection is locked while a turn
  // is prepared or active, in the handler as well as the control.
  const handleModelChange = useCallback((model: string) => {
    if (preparingRef.current || chat.hasActiveTurn()) return
    setPreferences(prev => ({
      ...prev,
      modelOverride: model || null,
    }))
  }, [setPreferences, chat])

  const handleSystemPromptModeChange = useCallback((mode: SystemPromptMode) => {
    if (preparingRef.current) return
    setPreferences(prev => {
      const nextCustom = mode === 'custom' && !prev.systemPromptCustom
        ? config.default_system
        : prev.systemPromptCustom
      return {
        ...prev,
        systemPromptMode: mode,
        systemPromptCustom: nextCustom,
      }
    })
  }, [config.default_system, setPreferences])

  const handleSystemPromptCustomChange = useCallback((value: string) => {
    if (preparingRef.current) return
    setPreferences(prev => ({
      ...prev,
      systemPromptCustom: value,
    }))
  }, [setPreferences])

  const setGuard = useCallback((held: boolean) => {
    preparingRef.current = held
    setPreparing(held)
  }, [])

  // the pre-send save barrier. the candidate -- the recovery projection, the
  // new user message, and its in-progress marker -- is private until the
  // store acknowledges it: it enters neither the working copy nor chat state
  // nor any mirrored snapshot before then. no chat request is issued unless
  // that specific save succeeds; on success the candidate is installed and
  // the request started for its original owner with no intervening await.
  const handleSend = useCallback(async (content: string): Promise<boolean> => {
    if (sessionsLoading || destructivePendingRef.current || preparingRef.current || chat.isStreaming) return false
    if (!canSend) return false
    setGuard(true)
    setSendError(null)

    // everything the request depends on is captured before the await.
    const creating = !activeId
    const ownerId = activeId ?? nanoid()
    const options = {
      model: selectedModel,
      systemPromptMode: preferences.systemPromptMode,
      systemPrompt: preferences.systemPromptCustom,
    }
    const now = Date.now()
    const base: Conversation = activeConversation?.doc ?? { title: content.slice(0, 50), messages: [], createdAt: now, updatedAt: now }
    const turnId = nanoid()
    const candidate = prepareTurn(chat.recordRef.current, content, turnId, options.model)
    const body = buildChatRequest(candidate, turnId, options.model, options.systemPromptMode, options.systemPrompt)
    const doc: Conversation = {
      ...base,
      messages: candidate.messages,
      turns: candidate.turns,
      // a new request resets the live usage record, as before.
      usage: null,
      title: base.title || extractTitle(candidate.messages),
      updatedAt: now,
    }

    const refuse = (message: string) => {
      setSendError(message)
      setGuard(false)
      return false
    }
    const problem = validateRecovery(body)
    if (problem) return refuse(`cannot send: ${problem.message}`)
    const bodySize = utf8ByteLength(JSON.stringify(body))
    if (bodySize > MAX_BODY_BYTES) return refuse(`cannot send: the request is ${bodySize} bytes, over the ${MAX_BODY_BYTES}-byte limit`)

    try {
      await saveCandidate(ownerId, doc)
    } catch (reason) {
      // the candidate is discarded: nothing local holds it, so no later
      // rename or unrelated save can persist its message or marker.
      return refuse(`not sent: the conversation could not be saved (${reason instanceof Error ? reason.message : String(reason)})`)
    }

    // a torn-down tab never sends; a marker already written stays on disk
    // and is conservatively recoverable on the next load.
    if (!mountedRef.current) return false
    if (activeIdRef.current !== (creating ? null : ownerId)) return refuse('not sent: the conversation changed while saving')

    installConversation(ownerId, doc)
    chatOwnerIdRef.current = ownerId
    savedSnapshotRef.current = { messages: doc.messages, usage: doc.usage ?? null, turns: doc.turns }
    if (creating) {
      // the sync effect's load of the stored document would restart chat
      // state this turn is about to own.
      skipNextConversationSyncRef.current = true
      activeIdRef.current = ownerId
      setActiveId(ownerId)
    }
    chat.startTurn(candidate, body, options.model)
    setGuard(false)
    return true
  }, [activeId, activeConversation, sessionsLoading, canSend, preferences, selectedModel, chat, saveCandidate, installConversation, setActiveId, setGuard])

  const handleRetry = useCallback(() => {
    if (recovery.kind !== 'retryable') return
    const original = chat.messages[recovery.record.user_message_index]
    if (original?.content) void handleSend(original.content)
  }, [recovery, chat.messages, handleSend])

  const handleReconcile = useCallback((decisions: Record<string, CallDecision>, note: string, execution: Execution) => {
    if (recovery.kind !== 'reconcile' || preparingRef.current || destructivePendingRef.current) return
    chat.reconcile(recovery.record.id, decisions, note, execution)
  }, [recovery, chat])

  const handleExportConversation = useCallback(() => {
    if (!activeConversation) return

    const exportConversation: Conversation = {
      ...activeConversation.doc,
      title: activeConversation.doc.title || extractTitle(chat.messages),
      messages: chat.messages,
      updatedAt: Date.now(),
    }
    if (!hasExportableMessages(exportConversation)) return

    const markdown = conversationToMarkdown(exportConversation)
    const filename = buildConversationMarkdownFilename(exportConversation)
    downloadMarkdown(filename, markdown)
  }, [activeConversation, chat.messages])

  return (
    <div className="app-layout">
      <Toolbar
        conversationsOpen={sidebarOpen}
        onToggleConversations={() => setSidebarOpen(!sidebarOpen)}
        canCreate={canNavigate}
        onNew={handleNewConversation}
        canExport={canExportActiveConversation}
        onExport={handleExportConversation}
        models={models}
        defaultModel={config.default_model}
        modelOverride={preferences.modelOverride || ''}
        onModelChange={handleModelChange}
        modelLocked={preparing || chat.isStreaming}
        promptLocked={preparing}
        mode={preferences.systemPromptMode}
        customValue={preferences.systemPromptCustom}
        defaultValue={config.default_system}
        onModeChange={handleSystemPromptModeChange}
        onCustomChange={handleSystemPromptCustomChange}
        toolsCount={tools.length}
        toolsOpen={toolPanelOpen}
        onToggleTools={() => setToolPanelOpen(!toolPanelOpen)}
        usage={activeConversation ? chat.usageRecord : null}
        selectedModel={selectedModel}
        contextWindows={config.context_windows}
        defaultContextWindow={config.default_context_window}
      />

      <div className="app-body">
        {sidebarOpen && (
          <aside className="sidebar">
            <ConversationList
              conversations={conversations}
              activeId={activeId}
              canCreate={canNavigate}
              onSelect={handleSelectConversation}
              onNew={handleNewConversation}
              onDelete={handleDeleteConversation}
              onRename={handleRenameConversation}
              locked={preparing}
            />
          </aside>
        )}

        <main className="main">
          <ChatView
            messages={chat.messages}
            isStreaming={chat.isStreaming}
            streamingContent={chat.streamingContent}
            streamingThinking={chat.streamingThinking}
            activeToolCalls={chat.activeToolCalls}
            error={chat.error}
            appError={appError}
            canSend={canSend}
            sendNotice={sendNotice}
            onSend={handleSend}
            preparing={preparing}
            recovery={recovery.kind === 'clear' ? null : (
              <RecoveryPanel
                key={recovery.record.id}
                assessment={recovery}
                disabled={!canNavigate}
                onRetry={handleRetry}
                onReconcile={handleReconcile}
              />
            )}
            onApprove={chat.approveToolCall}
            onDeny={chat.denyToolCall}
            onAbort={chat.abort}
            onToggleThinkingCollapsed={chat.setThinkingCollapsed}
          />
        </main>

        {toolPanelOpen && (
          <ToolPanel
            tools={tools}
            servers={servers}
            onClose={() => setToolPanelOpen(false)}
          />
        )}
      </div>
    </div>
  )
}

function extractTitle(messages: { role: string; content: string | null }[]): string {
  const first = messages.find(m => m.role === 'user')
  if (!first?.content) return 'New conversation'
  return first.content.slice(0, 50)
}

function migrateLegacyPreferences(): ChatPreferences | null {
  const modelOverride = readLegacyString('pane:model')
  const systemPromptCustom = readLegacyString('pane:systemPrompt')

  if (!modelOverride && !systemPromptCustom) {
    return null
  }

  localStorage.removeItem('pane:model')
  localStorage.removeItem('pane:systemPrompt')

  return {
    modelOverride,
    systemPromptMode: systemPromptCustom ? 'custom' : 'default',
    systemPromptCustom: systemPromptCustom || '',
  }
}

function readLegacyString(key: string): string | null {
  try {
    const raw = localStorage.getItem(key)
    if (raw === null) return null
    const parsed = JSON.parse(raw)
    return typeof parsed === 'string' && parsed.trim() ? parsed : null
  } catch {
    return null
  }
}
