import { useEffect, useState } from 'react'
import type { ConfigResponse } from '../types'

const emptyConfig: ConfigResponse = {
  default_model: '',
  default_system: '',
  mcp_separator: '_',
  context_windows: {},
  default_context_window: 0,
}

export function useConfig() {
  const [config, setConfig] = useState<ConfigResponse>(emptyConfig)
  const [loading, setLoading] = useState(true)
  // the failure is exposed rather than swallowed into emptyConfig: an empty
  // model list and the failing sends that follow would otherwise come with
  // nothing on the page that says why. no retry -- the client would be
  // hammering a fetch its own binary just failed to serve.
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetch('/api/config')
      .then(r => {
        if (!r.ok) throw new Error(r.statusText || `HTTP ${r.status}`)
        return r.json()
      })
      .then(data => {
        setConfig({
          default_model: data.default_model || '',
          default_system: data.default_system || '',
          mcp_separator: data.mcp_separator || '_',
          context_windows: data.context_windows || {},
          default_context_window: data.default_context_window || 0,
        })
      })
      .catch(reason => {
        const message = reason instanceof Error ? reason.message : String(reason)
        setError(`server config unavailable (${message}) — restart pane and reload`)
      })
      .finally(() => setLoading(false))
  }, [])

  return { config, loading, error }
}
