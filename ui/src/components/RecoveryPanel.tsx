import { useState } from 'react'
import type { Execution } from '../types'
import type { CallDecision, RecoveryAssessment } from '../lib/turnRecord'

interface Props {
  assessment: Exclude<RecoveryAssessment, { kind: 'clear' }>
  disabled: boolean
  // retry sends the interrupted turn's text again as a new turn; offered
  // only on affirmative evidence that nothing ran.
  onRetry: () => void
  onReconcile: (decisions: Record<string, CallDecision>, note: string, execution: Execution) => void
}

type Choice = 'none' | 'known' | 'unknown'

const choiceLabels: Record<Choice, string> = {
  none: 'did not run',
  known: 'ran (describe the outcome)',
  unknown: 'unknown; proceed without repeating it',
}

// the interruption surface: an interrupted turn is never resumed or resent
// on its own. the operator either retries a turn proven not to have run, or
// records what they know, which pane then places in history as attributed
// notes rather than as tool results.
export function RecoveryPanel({ assessment, disabled, onRetry, onReconcile }: Props) {
  const [decisions, setDecisions] = useState<Record<string, { execution: Choice; note: string }>>({})
  const [turnChoice, setTurnChoice] = useState<Choice>('none')
  const [turnNote, setTurnNote] = useState('')
  const record = assessment.record

  if (assessment.kind === 'retryable') {
    return (
      <div className="recovery-panel" role="region" aria-label="interrupted turn">
        <p>
          the last request stopped before any tool ran
          {record.error_code ? ` ('${record.error_code}')` : ''}.
        </p>
        <button className="recovery-btn" onClick={onRetry} disabled={disabled}>retry</button>
      </div>
    )
  }

  const ambiguous = assessment.ambiguous
  const knownWork = record.rounds.some(round => round.calls.some(call => call.state === 'completed' || call.state === 'failed'))
  const complete = ambiguous.every(call => decisions[call.id]?.note.trim())
    && (ambiguous.length > 0 || turnNote.trim() !== '')

  const submit = () => {
    if (!complete || disabled) return
    const out: Record<string, CallDecision> = {}
    for (const call of ambiguous) out[call.id] = { execution: decisions[call.id].execution, note: decisions[call.id].note }
    const execution: Execution = ambiguous.length > 0 || knownWork ? 'none' : turnChoice
    onReconcile(out, turnNote, execution)
  }

  return (
    <div className="recovery-panel" role="region" aria-label="interrupted turn">
      <p>
        the last turn was interrupted; pane cannot tell whether all of its tools ran.
        record what you know before continuing. nothing is resent or repeated.
      </p>
      {record.partial_text && (
        <blockquote className="recovery-partial">partial response, not recorded as model output: {record.partial_text}</blockquote>
      )}
      {ambiguous.map(call => (
        <div key={call.id} className="recovery-call">
          <div className="recovery-call-name">
            {call.function.name} <code>{call.function.arguments}</code> ({call.state})
          </div>
          <select
            aria-label={`outcome of ${call.id}`}
            value={decisions[call.id]?.execution ?? 'unknown'}
            disabled={disabled}
            onChange={e => setDecisions(prev => ({
              ...prev, [call.id]: { execution: e.target.value as Choice, note: prev[call.id]?.note ?? '' },
            }))}
          >
            {(Object.keys(choiceLabels) as Choice[]).map(choice => (
              <option key={choice} value={choice}>{choiceLabels[choice]}</option>
            ))}
          </select>
          <textarea
            aria-label={`note for ${call.id}`}
            placeholder="what you know about this call"
            value={decisions[call.id]?.note ?? ''}
            disabled={disabled}
            onChange={e => setDecisions(prev => ({
              ...prev, [call.id]: { execution: prev[call.id]?.execution ?? 'unknown', note: e.target.value },
            }))}
          />
        </div>
      ))}
      {ambiguous.length === 0 && !knownWork && (
        <select aria-label="turn outcome" value={turnChoice} disabled={disabled} onChange={e => setTurnChoice(e.target.value as Choice)}>
          <option value="none">no tools ran</option>
          <option value="unknown">unknown; proceed with uncertainty</option>
        </select>
      )}
      <textarea
        aria-label="reconciliation note"
        placeholder={ambiguous.length > 0 ? 'optional note for the whole turn' : 'what you know about this turn'}
        value={turnNote}
        disabled={disabled}
        onChange={e => setTurnNote(e.target.value)}
      />
      <button className="recovery-btn" onClick={submit} disabled={disabled || !complete}>record and continue</button>
    </div>
  )
}
