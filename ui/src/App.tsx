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
import {
  buildConversationMarkdownFilename,
  conversationToMarkdown,
  downloadMarkdown,
  hasExportableMessages,
} from './lib/exportMarkdown'
import type { Conversation, ChatPreferences, Message, SystemPromptMode, UsageRecord } from './types'

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
}

export default function App() {
  const {
    conversations,
    setConversations,
    loading: sessionsLoading,
    error: sessionsError,
    remove: removeConversation,
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

  const { config, error: configError } = useConfig()
  const { models } = useModels()
  const { tools, servers } = useTools()
  const chat = useChat()

  useEffect(() => {
    if (localStorage.getItem('pane:chatPreferences')) return

    const migrated = migrateLegacyPreferences()
    if (migrated) {
      setPreferences(migrated)
    }
  }, [setPreferences])

  const activeConversation = conversations.find(c => c.id === activeId)
  const selectedModel = preferences.modelOverride || config.default_model
  const canExportActiveConversation = activeConversation
    ? hasExportableMessages({ ...activeConversation.doc, messages: chat.messages })
    : false
  // the session gate: every session-mutating entry point is closed while
  // hydration is incomplete or a destructive operation is pending. loading
  // moves only true->false, so state suffices for that half.
  const canSend = !sessionsLoading && !destructivePending
  // the session error takes the line when present, so a persistent config
  // failure can never mask a live one.
  const appError = sessionsError ?? configError

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
      ? { messages: activeConversation.doc.messages, usage: activeConversation.doc.usage ?? null }
      : null
    chat.loadConversation(activeConversation ? activeConversation.doc : null)
  }, [activeId, sessionsLoading]) // eslint-disable-line react-hooks/exhaustive-deps

  // mirror chat state back to the conversation when it changes. a passive
  // effect: it writes, it does not paint.
  useEffect(() => {
    if (chatOwnerIdRef.current !== activeId) return
    if (!activeId || chat.messages.length === 0) return

    // the read-is-not-a-write guard: while chat holds the same references the
    // last mirrored document holds, selecting or reloading a conversation
    // emits no PUT, so the stored updatedAt and the rail's ordering are
    // untouched by a mere open.
    const snapshot = savedSnapshotRef.current
    if (snapshot && snapshot.messages === chat.messages && snapshot.usage === chat.usageRecord) return

    const messages = chat.messages
    const usage = chat.usageRecord
    savedSnapshotRef.current = { messages, usage }
    setConversations(prev => prev.map(c => {
      if (c.id !== activeId) return c
      return {
        ...c,
        doc: {
          ...c.doc,
          messages,
          usage,
          title: c.doc.title || extractTitle(messages),
          updatedAt: Date.now(),
        },
      }
    }))
  }, [chat.messages, chat.usageRecord]) // eslint-disable-line react-hooks/exhaustive-deps

  const handleNewConversation = useCallback(() => {
    if (sessionsLoading || destructivePendingRef.current) return
    chat.abort()

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
  }, [sessionsLoading, setConversations, setActiveId, chat])

  const handleSelectConversation = useCallback((id: string) => {
    // selecting during the destructive window would re-attach commit
    // ownership through the sync effect, and a message mutation on the newly
    // selected conversation would then commit a save behind the in-flight
    // delete, re-creating the file after the deletion with no error.
    if (destructivePendingRef.current) return
    if (id === activeId) return
    chat.abort()
    setActiveId(id)
  }, [activeId, chat, setActiveId])

  const handleDeleteConversation = useCallback((id: string) => {
    if (destructivePendingRef.current) return

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

  const handleModelChange = useCallback((model: string) => {
    setPreferences(prev => ({
      ...prev,
      modelOverride: model || null,
    }))
  }, [setPreferences])

  const handleSystemPromptModeChange = useCallback((mode: SystemPromptMode) => {
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
    setPreferences(prev => ({
      ...prev,
      systemPromptCustom: value,
    }))
  }, [setPreferences])

  const handleSend = useCallback((content: string) => {
    if (sessionsLoading || destructivePendingRef.current) return

    let id = activeId
    if (!id) {
      const newId = nanoid()
      const now = Date.now()
      const doc: Conversation = {
        title: content.slice(0, 50),
        messages: [],
        createdAt: now,
        updatedAt: now,
      }
      setConversations(prev => [{ id: newId, doc }, ...prev])
      setActiveId(newId)
      id = newId
      chatOwnerIdRef.current = newId
      // the sync effect's load of the empty document would clobber the turn
      // this send is about to start.
      skipNextConversationSyncRef.current = true
    } else {
      chatOwnerIdRef.current = id
    }
    chat.sendMessage(content, {
      model: selectedModel,
      systemPromptMode: preferences.systemPromptMode,
      systemPrompt: preferences.systemPromptCustom,
    })
  }, [activeId, sessionsLoading, preferences, selectedModel, chat, setConversations, setActiveId])

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
        canCreate={canSend}
        onNew={handleNewConversation}
        canExport={canExportActiveConversation}
        onExport={handleExportConversation}
        models={models}
        defaultModel={config.default_model}
        modelOverride={preferences.modelOverride || ''}
        onModelChange={handleModelChange}
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
              canCreate={canSend}
              onSelect={handleSelectConversation}
              onNew={handleNewConversation}
              onDelete={handleDeleteConversation}
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
            onSend={handleSend}
            onRetry={chat.retryLastRequest}
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
