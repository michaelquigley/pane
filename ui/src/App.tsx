import { useState, useCallback, useEffect } from 'react'
import { nanoid } from 'nanoid'
import { useLocalStorage } from './hooks/useLocalStorage'
import { useConfig } from './hooks/useConfig'
import { useModels } from './hooks/useModels'
import { useTools } from './hooks/useTools'
import { useChat } from './hooks/useChat'
import { ChatView } from './components/ChatView'
import { ConversationList } from './components/ConversationList'
import { ModelSelector } from './components/ModelSelector'
import { SystemPromptEditor } from './components/SystemPromptEditor'
import { ToolPanel } from './components/ToolPanel'
import type { Conversation, ChatPreferences, SystemPromptMode } from './types'

const defaultChatPreferences: ChatPreferences = {
  modelOverride: null,
  systemPromptMode: 'default',
  systemPromptCustom: '',
}

export default function App() {
  const [conversations, setConversations] = useLocalStorage<Conversation[]>('pane:conversations', [])
  const [activeId, setActiveId] = useLocalStorage<string | null>('pane:activeConversation', null)
  const [preferences, setPreferences] = useLocalStorage<ChatPreferences>('pane:chatPreferences', defaultChatPreferences)
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [toolPanelOpen, setToolPanelOpen] = useState(false)

  const { config } = useConfig()
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

  // sync chat messages when switching conversations
  useEffect(() => {
    if (activeConversation) {
      chat.setMessages(activeConversation.messages)
    } else {
      chat.setMessages([])
    }
  }, [activeId]) // eslint-disable-line react-hooks/exhaustive-deps

  // save messages back to conversation when they change
  useEffect(() => {
    if (!activeId || chat.messages.length === 0) return
    setConversations(prev => prev.map(c => {
      if (c.id !== activeId) return c
      const title = c.title || extractTitle(chat.messages)
      return { ...c, messages: chat.messages, title, updatedAt: Date.now() }
    }))
  }, [chat.messages]) // eslint-disable-line react-hooks/exhaustive-deps

  const handleNewConversation = useCallback(() => {
    const conv: Conversation = {
      id: nanoid(),
      title: '',
      messages: [],
      createdAt: Date.now(),
      updatedAt: Date.now(),
    }
    setConversations(prev => [conv, ...prev])
    setActiveId(conv.id)
  }, [setConversations, setActiveId])

  const handleDeleteConversation = useCallback((id: string) => {
    setConversations(prev => prev.filter(c => c.id !== id))
    if (activeId === id) {
      setActiveId(null)
      chat.setMessages([])
    }
  }, [activeId, setConversations, setActiveId, chat])

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
    if (!activeId) {
      // auto-create conversation
      const conv: Conversation = {
        id: nanoid(),
        title: content.slice(0, 50),
        messages: [],
        createdAt: Date.now(),
        updatedAt: Date.now(),
      }
      setConversations(prev => [conv, ...prev])
      setActiveId(conv.id)
      // small delay to let state propagate
      setTimeout(() => {
        chat.sendMessage(content, {
          model: preferences.modelOverride || '',
          systemPromptMode: preferences.systemPromptMode,
          systemPrompt: preferences.systemPromptCustom,
        })
      }, 0)
    } else {
      chat.sendMessage(content, {
        model: preferences.modelOverride || '',
        systemPromptMode: preferences.systemPromptMode,
        systemPrompt: preferences.systemPromptCustom,
      })
    }
  }, [activeId, preferences, chat, setConversations, setActiveId])

  return (
    <div className="app-layout">
      {sidebarOpen && (
        <aside className="sidebar">
          <ConversationList
            conversations={conversations}
            activeId={activeId}
            onSelect={setActiveId}
            onNew={handleNewConversation}
            onDelete={handleDeleteConversation}
          />
        </aside>
      )}

      <main className="main">
        <header className="header">
          <button className="header-btn" onClick={() => setSidebarOpen(!sidebarOpen)}>
            &#9776;
          </button>
          <ModelSelector
            models={models}
            defaultModel={config.default_model}
            selected={preferences.modelOverride || ''}
            onChange={handleModelChange}
          />
          <SystemPromptEditor
            mode={preferences.systemPromptMode}
            customValue={preferences.systemPromptCustom}
            defaultValue={config.default_system}
            onModeChange={handleSystemPromptModeChange}
            onCustomChange={handleSystemPromptCustomChange}
          />
          <div className="header-spacer" />
          <button className="header-btn" onClick={() => setToolPanelOpen(!toolPanelOpen)}>
            Tools{tools.length > 0 && ` (${tools.length})`}
          </button>
        </header>

        <ChatView
          messages={chat.messages}
          isStreaming={chat.isStreaming}
          streamingContent={chat.streamingContent}
          activeToolCalls={chat.activeToolCalls}
          error={chat.error}
          onSend={handleSend}
          onRetry={chat.retryLastRequest}
          onApprove={chat.approveToolCall}
          onDeny={chat.denyToolCall}
          onAbort={chat.abort}
        />
      </main>

      {toolPanelOpen && (
        <ToolPanel
          tools={tools}
          servers={servers}
          separator={config.mcp_separator}
          onClose={() => setToolPanelOpen(false)}
        />
      )}
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
