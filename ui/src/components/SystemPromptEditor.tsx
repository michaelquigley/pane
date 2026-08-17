import { useCallback, useEffect, useRef, useState } from 'react'
import { DescriptionIcon } from './icons'
import type { SystemPromptMode } from '../types'

interface Props {
  mode: SystemPromptMode
  customValue: string
  defaultValue: string
  onModeChange: (value: SystemPromptMode) => void
  onCustomChange: (value: string) => void
}

// a toolbar-mount control: the description glyph on the bar is the only door
// to the system prompt's surface. it lights on the non-default modes and its
// label names the current mode; the glyph opens a modal holding the mode
// select and, for custom, the text. the mode changes only inside the modal,
// so the glyph never auto-opens — its lit state is the resting signal.
export function SystemPromptEditor({
  mode,
  customValue,
  defaultValue,
  onModeChange,
  onCustomChange,
}: Props) {
  const [open, setOpen] = useState(false)
  const glyphRef = useRef<HTMLButtonElement>(null)
  const cardRef = useRef<HTMLDivElement>(null)

  const closeModal = useCallback(() => {
    setOpen(false)
  }, [])

  // while open, focus starts in the card, wraps inside it, and escape or an
  // outside click (the backdrop) closes the modal and returns focus to the
  // glyph.
  useEffect(() => {
    if (!open) return

    const glyph = glyphRef.current
    const card = cardRef.current
    if (card) {
      const first = card.querySelector<HTMLElement>('select, textarea, button')
      first?.focus()
    }

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        setOpen(false)
        return
      }
      if (e.key !== 'Tab') return

      const panel = cardRef.current
      if (!panel) return

      const focusables = Array.from(
        panel.querySelectorAll<HTMLElement>('select, textarea, button, a[href]'),
      ).filter(el => !el.hasAttribute('disabled'))
      if (focusables.length === 0) return

      const first = focusables[0]
      const last = focusables[focusables.length - 1]
      const active = document.activeElement

      if (!panel.contains(active)) {
        e.preventDefault()
        first.focus()
        return
      }
      if (e.shiftKey && active === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && active === last) {
        e.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('keydown', handleKeyDown)
      glyph?.focus()
    }
  }, [open])

  return (
    <>
      <button
        ref={glyphRef}
        className={`toolbar-link toolbar-glyph${mode !== 'default' ? ' lit' : ''}`}
        title={`system prompt (${mode})`}
        aria-label={`system prompt (${mode})`}
        onClick={() => setOpen(true)}
      >
        <DescriptionIcon />
      </button>

      {open && (
        <>
          <div className="system-prompt-backdrop" onClick={closeModal} />
          <div
            className="system-prompt-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="system-prompt-modal-title"
            ref={cardRef}
          >
            <div className="system-prompt-modal-body">
              <h3 id="system-prompt-modal-title">System prompt</h3>
              <select
                className="system-prompt-mode"
                value={mode}
                onChange={e => onModeChange(e.target.value as SystemPromptMode)}
              >
                <option value="default">use default</option>
                <option value="custom">use custom</option>
                <option value="none">send none</option>
              </select>

              {mode === 'custom' ? (
                <textarea
                  className="system-prompt-textarea"
                  value={customValue}
                  onChange={e => onCustomChange(e.target.value)}
                  placeholder={defaultValue || 'Enter system prompt...'}
                  rows={12}
                />
              ) : mode === 'none' ? (
                <div className="system-prompt-note">No system prompt will be sent.</div>
              ) : (
                <div className="system-prompt-note">
                  {defaultValue || 'No configured system prompt.'}
                </div>
              )}
            </div>
          </div>
        </>
      )}
    </>
  )
}