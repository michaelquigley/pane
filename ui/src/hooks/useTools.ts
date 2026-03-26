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
  return { tools, servers, loading, refreshTools: fetchTools }
}
