package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const restartChildTurn = "PANE_RESTART_CHILD_TURN"

// runRestartChild runs one generic tool turn in this process and prints its
// recorded events for the parent.
func runRestartChild(t *testing.T, turn string) {
	h := newTurnHarness(t)
	h.chat.script = []string{chatToolCall("add", `{}`), chatText("42")}
	recorder, events := h.post(t, strings.ReplaceAll(freshTurn("qwen", "add"), `"t1"`, `"`+turn+`"`))
	if recorder.Code != http.StatusOK || h.tools.count() != 1 {
		t.Fatalf("child turn failed: status=%d executions=%d", recorder.Code, h.tools.count())
	}
	encoded, _ := json.Marshal(events)
	os.Stdout.WriteString("RESTART_EVENTS=" + base64.StdEncoding.EncodeToString(encoded) + "\n")
}

// restartTurn launches the test binary as a fresh process for one turn.
func restartTurn(t *testing.T, turn string) []recordedEvent {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestGenericCallIDsSurviveProcessRestart$")
	command.Env = append(os.Environ(), restartChildTurn+"="+turn)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("child process: %v\n%s", err, output)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if encoded, ok := strings.CutPrefix(line, "RESTART_EVENTS="); ok {
			raw, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatal(err)
			}
			var events []recordedEvent
			if err := json.Unmarshal(raw, &events); err != nil {
				t.Fatal(err)
			}
			return events
		}
	}
	t.Fatalf("child process reported no events:\n%s", output)
	return nil
}

// generic call ids minted before and after a restart must not collide: the
// recovery intake rejects duplicate call ids across prior turns, so a
// colliding pair would block the conversation's next message.
func TestGenericCallIDsSurviveProcessRestart(t *testing.T) {
	if turn := os.Getenv(restartChildTurn); turn != "" {
		runRestartChild(t, turn)
		return
	}

	var messages []any
	var records []any
	var ids []string
	for _, turn := range []string{"before_restart", "after_restart"} {
		events := restartTurn(t, turn)
		userIndex := len(messages)
		messages = append(messages, map[string]any{"role": "user", "content": "add", "turn_id": turn})
		var rounds []any
		var terminal any
		lastSeq := 0.0
		observed := map[string][]string{}
		for _, event := range events {
			if seq, ok := event.Data["seq"].(float64); ok {
				lastSeq = seq
			}
			switch event.Type {
			case "tool_call_start", "tool_call_result":
				observed[event.Type] = append(observed[event.Type], event.Data["id"].(string))
			case "round_ready":
				for _, call := range toolCalls(event.Data["assistant"]) {
					observed["round_ready"] = append(observed["round_ready"], call["id"].(string))
				}
			case "round_complete":
				assistant := event.Data["assistant"].(map[string]any)
				calls := []any{}
				for _, call := range toolCalls(assistant) {
					observed["round_complete"] = append(observed["round_complete"], call["id"].(string))
					calls = append(calls, map[string]any{"id": call["id"], "type": "function", "function": call["function"],
						"state": "completed", "result": map[string]any{"content": "42", "is_error": false}})
				}
				for _, tool := range event.Data["tool_messages"].([]any) {
					observed["tool_message"] = append(observed["tool_message"], tool.(map[string]any)["tool_call_id"].(string))
				}
				rounds = append(rounds, map[string]any{"round_id": event.Data["round_id"], "message_index": len(messages), "calls": calls, "committed": true})
				messages = append(messages, assistant)
				messages = append(messages, event.Data["tool_messages"].([]any)...)
			case "turn_end":
				terminal = map[string]any{"outcome": event.Data["outcome"], "execution": event.Data["execution"]}
			}
		}
		// the preview, finalized call, result, and tool message all carry the
		// one id minted for the call.
		preview := observed["tool_call_start"]
		if len(preview) != 1 {
			t.Fatalf("%s: expected one previewed call, got %v", turn, observed)
		}
		for _, kind := range []string{"round_ready", "tool_call_result", "round_complete", "tool_message"} {
			if len(observed[kind]) != 1 || observed[kind][0] != preview[0] {
				t.Fatalf("%s: %s id %v disagrees with preview %v", turn, kind, observed[kind], preview)
			}
		}
		ids = append(ids, preview[0])
		records = append(records, map[string]any{"id": turn, "v": 1, "model_alias": "qwen", "user_message_index": userIndex,
			"state": "completed", "last_seq": lastSeq, "rounds": rounds, "terminal": terminal})
	}
	if ids[0] == ids[1] {
		t.Fatalf("call ids collided across a restart: %s", ids[0])
	}

	messages = append(messages, map[string]any{"role": "user", "content": "continue", "turn_id": "next"})
	body, _ := json.Marshal(map[string]any{"model": "qwen", "turn_id": "next", "messages": messages, "recovery": map[string]any{"v": 1, "turns": records}})
	h := newTurnHarness(t)
	recorder, _ := h.post(t, string(body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("history from two processes rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	// the saved ids reach the provider unchanged, each as its call and its
	// result binding.
	sent := h.chat.requests()[0]
	for _, id := range ids {
		if strings.Count(sent, `"`+id+`"`) != 2 {
			t.Fatalf("saved call id %s was not carried unchanged: %s", id, sent)
		}
	}
}

func toolCalls(assistant any) []map[string]any {
	var calls []map[string]any
	raw, _ := assistant.(map[string]any)["tool_calls"].([]any)
	for _, call := range raw {
		calls = append(calls, call.(map[string]any))
	}
	return calls
}
