package api

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/llm"
)

// the portable recovery projection's generated text. the browser produces
// the same strings; intake checks them exactly against the recorded evidence.
const (
	recoveryNotExecutedText      = "pane recovery: this call was not executed."
	recoveryUnknownText          = "pane recovery: execution outcome unknown; no result was received."
	recoveryOperatorReportedText = "pane recovery: no tool result was received; the outcome below is operator-reported."
	recoveryOperatorSuffix       = "\noperator note: "
	recoveryNotePrefix           = "operator reconciliation: "

	placeholderNotExecuted      = "not_executed"
	placeholderUnknown          = "unknown"
	placeholderOperatorReported = "operator_reported"
)

// recoveryProjection is the browser's prior turn records, submitted beside
// the full history. it is pane-only input: validated here, never forwarded to
// a provider, and never authority to execute a historical call.
type recoveryProjection struct {
	V     int
	Turns []turnRecord
	// malformed records a projection whose shape could not be bound; intake
	// reports it as 'invalid_recovery' rather than as a bad request body.
	malformed string
}

type recoveryProjectionWire struct {
	V     int
	Turns []turnRecord
}

// UnmarshalDd requires an explicit turns array: an empty array means no
// prior records, while an absent one is a malformed projection.
func (p *recoveryProjection) UnmarshalDd(data map[string]any) error {
	if _, ok := data["turns"].([]any); !ok {
		p.malformed = "recovery turns must be an array"
		return nil
	}
	var wire recoveryProjectionWire
	if err := dd.Bind(&wire, data); err != nil {
		p.malformed = fmt.Sprintf("malformed recovery projection: %v", err)
		return nil
	}
	p.V, p.Turns = wire.V, wire.Turns
	return nil
}

type turnRecord struct {
	ID               string
	V                int
	ModelAlias       string
	UserMessageIndex *int
	State            string
	LastSeq          int
	Rounds           []roundRecord
	Origin           *llm.RoundOrigin
	ErrorCode        string
	Terminal         *turnTerminal
	Reconciliation   *turnReconciliation
}

type turnTerminal struct {
	Outcome   string
	Execution string
}

type turnReconciliation struct {
	Execution    string
	Note         string
	MessageIndex *int
}

type roundRecord struct {
	RoundID      string
	Assistant    *llm.Message
	MessageIndex *int
	Calls        []callRecord
	Committed    bool
}

type callRecord struct {
	ID             string
	Type           string
	Function       llm.ToolCallFunction
	State          string
	Result         *callResult
	Reconciliation *callReconciliation
}

type callResult struct {
	Content string
	IsError bool
}

type callReconciliation struct {
	Execution string
	Note      string
}

// recoveryError is a typed intake rejection: 'invalid_recovery' for a
// malformed or inconsistent projection, 'recovery_required' for an
// unresolved interruption the operator must reconcile first.
type recoveryError struct {
	Code    string
	Message string
}

func (e *recoveryError) Error() string { return e.Message }

func invalidRecovery(format string, args ...any) *recoveryError {
	return &recoveryError{Code: "invalid_recovery", Message: fmt.Sprintf(format, args...)}
}

func recoveryRequired(format string, args ...any) *recoveryError {
	return &recoveryError{Code: "recovery_required", Message: fmt.Sprintf(format, args...)}
}

var recoveryIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

func validRecoveryID(id string) bool { return recoveryIDPattern.MatchString(id) }

func knownCallState(state string) bool {
	switch state {
	case "pending", "denied", "rejected", "not_dispatched", "dispatched", "completed", "failed", "unknown":
		return true
	}
	return false
}

// ambiguousCallState names the states whose execution the record cannot
// establish on its own.
func ambiguousCallState(state string) bool {
	return state == "pending" || state == "dispatched" || state == "unknown"
}

func knownNonExecution(state string) bool {
	return state == "denied" || state == "rejected" || state == "not_dispatched"
}

func receivedState(state string) bool { return state == "completed" || state == "failed" }

func validExecution(execution string) bool {
	return execution == llm.ExecutionNone || execution == llm.ExecutionKnown || execution == llm.ExecutionUnknown
}

// validateRecovery checks the submitted history against its prior turn
// records before any provider or executor activity. indices refer to the
// submitted messages, before system-prompt normalization. these are
// consistency checks on client-supplied history, not authentication of the
// browser's claims or of external tool effects.
func validateRecovery(messages []llm.Message, fresh string, projection *recoveryProjection) *recoveryError {
	if projection.malformed != "" {
		return invalidRecovery("%s", projection.malformed)
	}
	if !validRecoveryID(fresh) {
		return invalidRecovery("invalid turn id")
	}
	if projection.V != 1 {
		return invalidRecovery("unsupported recovery version '%d'", projection.V)
	}
	if len(messages) == 0 {
		return invalidRecovery("no messages submitted")
	}
	last := len(messages) - 1
	final := messages[last]
	if final.TurnID != fresh || final.Role != "user" || final.RoundID != "" || len(final.ToolCalls) != 0 ||
		final.ToolCallID != "" || final.Origin != nil || final.Continuation != nil || final.RecoveryPlaceholder != "" {
		return invalidRecovery("the final message must be the fresh turn's plain user message")
	}
	for i := 0; i < last; i++ {
		if messages[i].TurnID == fresh {
			return invalidRecovery("fresh turn id appears on message %d", i)
		}
	}

	v := &recoveryValidator{messages: messages, last: last, claimed: make([]string, len(messages)),
		rounds: make(map[string]bool), calls: make(map[string]bool)}
	records := make(map[string]*turnRecord, len(projection.Turns))
	for i := range projection.Turns {
		record := &projection.Turns[i]
		if !validRecoveryID(record.ID) {
			return invalidRecovery("invalid turn record id")
		}
		if record.ID == fresh {
			return invalidRecovery("a prior record reuses the fresh turn id")
		}
		if records[record.ID] != nil {
			return invalidRecovery("duplicate turn record '%s'", record.ID)
		}
		records[record.ID] = record
		if err := v.record(record); err != nil {
			return err
		}
	}

	for i := 0; i < last; i++ {
		message := messages[i]
		if message.TurnID == "" {
			if message.RoundID != "" || message.RecoveryPlaceholder != "" {
				return invalidRecovery("message %d carries recovery metadata without a turn", i)
			}
			continue
		}
		if records[message.TurnID] == nil {
			return invalidRecovery("message %d names turn '%s' with no prior record", i, message.TurnID)
		}
		if v.claimed[i] != message.TurnID {
			return invalidRecovery("message %d is not bound by its turn record", i)
		}
	}

	for i := range projection.Turns {
		if err := admissible(&projection.Turns[i]); err != nil {
			return err
		}
	}
	return nil
}

type recoveryValidator struct {
	messages []llm.Message
	last     int
	claimed  []string
	rounds   map[string]bool
	calls    map[string]bool
}

func (v *recoveryValidator) claim(index *int, owner string) (int, *recoveryError) {
	if index == nil {
		return 0, invalidRecovery("turn '%s' is missing a message index", owner)
	}
	i := *index
	if i < 0 || i >= v.last {
		return 0, invalidRecovery("turn '%s' references message %d outside prior history", owner, i)
	}
	if v.claimed[i] != "" {
		return 0, invalidRecovery("message %d is bound twice", i)
	}
	v.claimed[i] = owner
	return i, nil
}

func (v *recoveryValidator) record(record *turnRecord) *recoveryError {
	if record.V != 1 {
		return invalidRecovery("unsupported turn record version '%d'", record.V)
	}
	switch record.State {
	case "in_progress", "completed", "interrupted":
	default:
		return invalidRecovery("turn '%s' has unknown state '%s'", record.ID, record.State)
	}
	if record.Terminal != nil {
		switch record.Terminal.Outcome {
		case llm.TurnCompleted, llm.TurnFailed, llm.TurnCancelled:
		default:
			return invalidRecovery("turn '%s' has unknown terminal outcome", record.ID)
		}
		if !validExecution(record.Terminal.Execution) {
			return invalidRecovery("turn '%s' has unknown terminal execution", record.ID)
		}
	}
	userIndex, err := v.claim(record.UserMessageIndex, record.ID)
	if err != nil {
		return err
	}
	user := v.messages[userIndex]
	if user.Role != "user" || user.TurnID != record.ID || user.RoundID != "" || len(user.ToolCalls) != 0 || user.RecoveryPlaceholder != "" {
		return invalidRecovery("turn '%s' user message index does not name its user message", record.ID)
	}

	terminalNone := terminalNonExecution(record)
	position := userIndex
	for r := range record.Rounds {
		round := &record.Rounds[r]
		if !validRecoveryID(round.RoundID) || v.rounds[round.RoundID] {
			return invalidRecovery("turn '%s' has an invalid or duplicate round id", record.ID)
		}
		v.rounds[round.RoundID] = true
		for c := range round.Calls {
			call := &round.Calls[c]
			if err := v.call(record.ID, call); err != nil {
				return err
			}
		}
		if round.MessageIndex == nil {
			if round.Committed || round.Assistant == nil {
				return invalidRecovery("round '%s' is neither represented in history nor retained as evidence", round.RoundID)
			}
			if !sameCalls(round.Assistant.ToolCalls, round.Calls) {
				return invalidRecovery("round '%s' evidence disagrees with its calls", round.RoundID)
			}
			continue
		}
		index, err := v.claim(round.MessageIndex, record.ID)
		if err != nil {
			return err
		}
		if index <= position {
			return invalidRecovery("round '%s' is out of order", round.RoundID)
		}
		assistant := v.messages[index]
		if assistant.Role != "assistant" || assistant.TurnID != record.ID || assistant.RoundID != round.RoundID ||
			assistant.RecoveryPlaceholder != "" || !sameCalls(assistant.ToolCalls, round.Calls) {
			return invalidRecovery("round '%s' does not match its assistant message", round.RoundID)
		}
		if round.Committed {
			if round.Assistant != nil {
				return invalidRecovery("committed round '%s' retains a duplicate pending assistant", round.RoundID)
			}
		} else if round.Assistant == nil || !sameAssistant(*round.Assistant, assistant) {
			return invalidRecovery("projected round '%s' does not match its retained assistant", round.RoundID)
		}
		for c := range round.Calls {
			call := &round.Calls[c]
			at := index + 1 + c
			if at >= v.last {
				return invalidRecovery("round '%s' is missing tool results", round.RoundID)
			}
			toolIndex, err := v.claim(&at, record.ID)
			if err != nil {
				return err
			}
			tool := v.messages[toolIndex]
			if tool.Role != "tool" || tool.TurnID != record.ID || tool.RoundID != round.RoundID || tool.ToolCallID != call.ID {
				return invalidRecovery("round '%s' tool results are missing, extra, or out of order", round.RoundID)
			}
			if err := toolContent(round, call, tool, terminalNone); err != nil {
				return err
			}
		}
		position = index + len(round.Calls)
	}

	if record.Reconciliation != nil {
		recon := record.Reconciliation
		if !validExecution(recon.Execution) || !strings.HasPrefix(recon.Note, recoveryNotePrefix) || len(recon.Note) == len(recoveryNotePrefix) {
			return invalidRecovery("turn '%s' has a malformed reconciliation", record.ID)
		}
		index, err := v.claim(recon.MessageIndex, record.ID)
		if err != nil {
			return err
		}
		if index <= position {
			return invalidRecovery("turn '%s' reconciliation note must follow its tool results", record.ID)
		}
		note := v.messages[index]
		if note.Role != "user" || note.TurnID != record.ID || note.RoundID != "" || len(note.ToolCalls) != 0 ||
			note.RecoveryPlaceholder != "" || content(note) != recon.Note {
			return invalidRecovery("turn '%s' reconciliation note does not match its message", record.ID)
		}
	}
	return nil
}

func (v *recoveryValidator) call(turn string, call *callRecord) *recoveryError {
	if !validRecoveryID(call.ID) || v.calls[call.ID] {
		return invalidRecovery("turn '%s' has an invalid or duplicate call id", turn)
	}
	v.calls[call.ID] = true
	if !knownCallState(call.State) {
		return invalidRecovery("call '%s' has unknown state '%s'", call.ID, call.State)
	}
	if receivedState(call.State) != (call.Result != nil) {
		return invalidRecovery("call '%s' result contradicts its state", call.ID)
	}
	if call.Result != nil && call.Result.IsError != (call.State == "failed") {
		return invalidRecovery("call '%s' result error flag contradicts its state", call.ID)
	}
	if call.Reconciliation != nil {
		if !ambiguousCallState(call.State) {
			return invalidRecovery("call '%s' reconciles an outcome that is already known", call.ID)
		}
		if !validExecution(call.Reconciliation.Execution) || strings.TrimSpace(call.Reconciliation.Note) == "" {
			return invalidRecovery("call '%s' has a malformed reconciliation", call.ID)
		}
	}
	return nil
}

// terminalNonExecution reports affirmative terminal evidence that no call in
// the turn ran: an observed turn_end with execution 'none' and no recorded
// call outcome contradicting it.
func terminalNonExecution(record *turnRecord) bool {
	if record.Terminal == nil || record.Terminal.Execution != llm.ExecutionNone {
		return false
	}
	for _, round := range record.Rounds {
		for _, call := range round.Calls {
			if call.State != "pending" && !knownNonExecution(call.State) {
				return false
			}
		}
	}
	return true
}

// toolContent checks one tool-role message against the portable recovery
// projection table for its call.
func toolContent(round *roundRecord, call *callRecord, tool llm.Message, terminalNone bool) *recoveryError {
	text := content(tool)
	if call.Result != nil {
		if tool.RecoveryPlaceholder != "" || text != call.Result.Content {
			return invalidRecovery("call '%s' tool message does not carry its received result", call.ID)
		}
		return nil
	}
	if round.Committed {
		// a normally promoted round carries the loop's own messages: received
		// results above, and loop-owned non-execution reasons here.
		if tool.RecoveryPlaceholder != "" || !knownNonExecution(call.State) {
			return invalidRecovery("committed round '%s' has an unresolved call '%s'", round.RoundID, call.ID)
		}
		return nil
	}
	want, kind := "", ""
	switch {
	case knownNonExecution(call.State):
		want, kind = recoveryNotExecutedText, placeholderNotExecuted
	case call.Reconciliation != nil:
		suffix := recoveryOperatorSuffix + call.Reconciliation.Note
		switch call.Reconciliation.Execution {
		case llm.ExecutionNone:
			want, kind = recoveryNotExecutedText+suffix, placeholderNotExecuted
		case llm.ExecutionUnknown:
			want, kind = recoveryUnknownText+suffix, placeholderUnknown
		default:
			want, kind = recoveryOperatorReportedText+suffix, placeholderOperatorReported
		}
	case call.State == "pending" && terminalNone:
		want, kind = recoveryNotExecutedText, placeholderNotExecuted
	default:
		return invalidRecovery("call '%s' has a recovery placeholder without supporting evidence", call.ID)
	}
	if tool.RecoveryPlaceholder != kind || text != want {
		return invalidRecovery("call '%s' recovery placeholder does not match its evidence", call.ID)
	}
	return nil
}

// admissible decides whether a well-formed record may precede a new turn.
func admissible(record *turnRecord) *recoveryError {
	switch record.State {
	case "in_progress":
		return recoveryRequired("turn '%s' is still marked in progress", record.ID)
	case "completed":
		if record.Terminal == nil || record.Terminal.Outcome != llm.TurnCompleted {
			return invalidRecovery("completed turn '%s' lacks successful terminal evidence", record.ID)
		}
		for _, round := range record.Rounds {
			if !round.Committed {
				return invalidRecovery("completed turn '%s' has an uncommitted round", record.ID)
			}
			for _, call := range round.Calls {
				if ambiguousCallState(call.State) || call.Reconciliation != nil {
					return invalidRecovery("completed turn '%s' has an unresolved call", record.ID)
				}
			}
		}
		if record.Reconciliation != nil {
			return invalidRecovery("completed turn '%s' carries a reconciliation", record.ID)
		}
		return nil
	}

	terminalNone := terminalNonExecution(record)
	unknown, known := false, false
	for _, round := range record.Rounds {
		if round.MessageIndex == nil {
			return recoveryRequired("turn '%s' has a finalized round not yet represented in history", record.ID)
		}
		for _, call := range round.Calls {
			if receivedState(call.State) {
				known = true
			}
			if call.Reconciliation != nil {
				if record.Reconciliation == nil || !strings.Contains(record.Reconciliation.Note, call.ID+": "+call.Reconciliation.Note) {
					return invalidRecovery("call '%s' reconciliation is not part of its turn's note", call.ID)
				}
				switch call.Reconciliation.Execution {
				case llm.ExecutionUnknown:
					unknown = true
				case llm.ExecutionKnown:
					known = true
				}
				continue
			}
			if ambiguousCallState(call.State) && !(call.State == "pending" && terminalNone) {
				return recoveryRequired("call '%s' has an unresolved outcome", call.ID)
			}
		}
	}
	if record.Reconciliation == nil {
		if !terminalNone {
			return recoveryRequired("interrupted turn '%s' needs reconciliation", record.ID)
		}
		return nil
	}
	execution := record.Reconciliation.Execution
	if unknown && execution != llm.ExecutionUnknown || known && execution == llm.ExecutionNone {
		return invalidRecovery("turn '%s' reconciliation understates its recorded evidence", record.ID)
	}
	return nil
}

func content(message llm.Message) string {
	if message.Content == nil {
		return ""
	}
	return *message.Content
}

func sameCalls(toolCalls []llm.ToolCall, calls []callRecord) bool {
	if len(toolCalls) != len(calls) {
		return false
	}
	for i, call := range calls {
		toolCall := toolCalls[i]
		if toolCall.ID != call.ID || toolCall.Function.Name != call.Function.Name || toolCall.Function.Arguments != call.Function.Arguments {
			return false
		}
	}
	return true
}

// sameAssistant compares a projected round's retained evidence with the
// assistant message placed in history: content, calls, origin, and the
// continuation envelope's decoded fields and items.
func sameAssistant(a, b llm.Message) bool {
	if a.Role != "assistant" || content(a) != content(b) || len(a.ToolCalls) != len(b.ToolCalls) {
		return false
	}
	for i := range a.ToolCalls {
		if a.ToolCalls[i].ID != b.ToolCalls[i].ID || a.ToolCalls[i].Function != b.ToolCalls[i].Function {
			return false
		}
	}
	if (a.Origin == nil) != (b.Origin == nil) || a.Origin != nil && *a.Origin != *b.Origin {
		return false
	}
	if (a.Continuation == nil) != (b.Continuation == nil) {
		return false
	}
	if a.Continuation == nil {
		return true
	}
	x, y := a.Continuation, b.Continuation
	if x.Format != y.Format || x.Version != y.Version || x.Identity != y.Identity || len(x.Items) != len(y.Items) || len(x.Bindings) != len(y.Bindings) {
		return false
	}
	for i := range x.Items {
		if !bytes.Equal(x.Items[i], y.Items[i]) {
			return false
		}
	}
	for i := range x.Bindings {
		if x.Bindings[i] != y.Bindings[i] {
			return false
		}
	}
	return true
}
