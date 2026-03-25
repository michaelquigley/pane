import { useState, useEffect, useCallback } from 'react'
import type { ToolInfo, ServerStatus } from '../types'

export function useTools() {
  const [tools, setTools] = useState<ToolInfo[]>([])
  const [servers, setServers] = useState<Record<string, ServerStatus>>({})
  const [loading, setLoading] = useState(true)

  const fetchTools = useCallback(() => {
    fetch('/api/tools')
      .then(r => r.json())
      .then(data => {
        setTools(data.tools || [])
        setServers(data.servers || {})
      })
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { fetchTools() }, [fetchTools])

  const toggleTool = useCallback((name: string, enabled: boolean) => {
    fetch('/api/tools/toggle', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool: name, enabled }),
    }).then(() => fetchTools())
  }, [fetchTools])

  const disabledTools = tools.filter(t => !t.enabled).map(t => t.function.name)

  return { tools, servers, disabledTools, toggleTool, loading }
}
