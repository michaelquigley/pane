import { useState, useCallback, useEffect } from 'react'
import { nanoid } from 'nanoid'
import { useLocalStorage } from './hooks/useLocalStorage'
import { useModels } from './hooks/useModels'
import { useTools } from './hooks/useTools'
import { useChat } from './hooks/useChat'
import { ChatView } from './components/ChatView'
import { ConversationList } from './components/ConversationList'
import { ModelSelector } from './components/ModelSelector'
import { SystemPromptEditor } from './components/SystemPromptEditor'
import { ToolPanel } from './components/ToolPanel'
import type { Conversation } from './types'

export default function App() {
  const [conversations, setConversations] = useLocalStorage<Conversation[]>('pane:conversations', [])
  const [activeId, setActiveId] = useLocalStorage<string | null>('pane:activeConversation', null)
  const [systemPrompt, setSystemPrompt] = useLocalStorage<string>('pane:systemPrompt', '')
  const [configLoaded, setConfigLoaded] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [toolPanelOpen, setToolPanelOpen] = useState(false)

  const { models, selectedModel, setSelectedModel } = useModels()

  // seed system prompt from backend config if user hasn't customized it
  useEffect(() => {
    if (configLoaded) return
    fetch('/api/config')
      .then(r => r.json())
      .then(data => {
        if (data.system && !systemPrompt) {
          setSystemPrompt(data.system)
        }
        setConfigLoaded(true)
      })
      .catch(() => setConfigLoaded(true))
  }, []) // eslint-disable-line react-hooks/exhaustive-deps
  const { tools, servers, disabledTools, toggleTool } = useTools()
  const chat = useChat()

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
        chat.sendMessage(content, selectedModel, systemPrompt, disabledTools)
      }, 0)
    } else {
      chat.sendMessage(content, selectedModel, systemPrompt, disabledTools)
    }
  }, [activeId, selectedModel, systemPrompt, disabledTools, chat, setConversations, setActiveId])

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
            selected={selectedModel}
            onChange={setSelectedModel}
          />
          <SystemPromptEditor value={systemPrompt} onChange={setSystemPrompt} />
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
          onApprove={chat.approveToolCall}
          onDeny={chat.denyToolCall}
          onAbort={chat.abort}
        />
      </main>

      {toolPanelOpen && (
        <ToolPanel
          tools={tools}
          servers={servers}
          onToggle={toggleTool}
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
