import { useState, useEffect, useCallback, useRef } from 'react'
import type { ModelInfo } from '../types'

const LOGIN_POLL_MS = 5000

// the model list with its local availability. it refreshes on window focus,
// on explicit request, and after each chat request; while the selected model
// waits for a cli login it polls every five seconds in a visible tab. listing
// is local on the backend: no refresh reaches a provider.
export function useModels(selected: string) {
  const [models, setModels] = useState<ModelInfo[]>([])
  const [loading, setLoading] = useState(true)
  const inFlightRef = useRef(false)
  const mountedRef = useRef(true)

  const refresh = useCallback(() => {
    // one fetch at a time: a focus event during a poll adds nothing.
    if (inFlightRef.current) return
    inFlightRef.current = true
    fetch('/api/models')
      .then(r => r.json())
      .then(data => {
        if (mountedRef.current) setModels(data.data || [])
      })
      .catch(() => {
        if (mountedRef.current) setModels([])
      })
      .finally(() => {
        inFlightRef.current = false
        if (mountedRef.current) setLoading(false)
      })
  }, [])

  useEffect(() => {
    mountedRef.current = true
    refresh()
    window.addEventListener('focus', refresh)
    return () => {
      mountedRef.current = false
      window.removeEventListener('focus', refresh)
    }
  }, [refresh])

  const waitingForLogin = models.find(model => model.id === selected)?.auth_state === 'login_required'

  useEffect(() => {
    if (!waitingForLogin) return
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') refresh()
    }, LOGIN_POLL_MS)
    return () => window.clearInterval(timer)
  }, [waitingForLogin, refresh])

  return { models, loading, refresh }
}
