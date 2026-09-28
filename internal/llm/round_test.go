package llm

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/michaelquigley/df/dd"
)

// testEnvelopeJSON is a strict-intake fixture carrying unknown keys at every
// named level, an explicit null inside identity, an escaped encrypted
// string, an integer beyond float64's exact range, and a fractional number.
const testEnvelopeJSON = `{"format":"codex-responses-items","v":1,"mystery_root":"z","identity":{"provider":"openai-codex","protocol":"responses","upstream_model":"gpt-5.6-sol","service":"https://chatgpt.com/backend-api/codex/responses","profile":"p1","account_scope":"chatgpt:ea1e30f202148af5945f8c85","mystery_identity":"x","null_field":null},"bindings":[{"pane_call_id":"pane_1","provider_call_id":"call_1","provider_item_id":"fc_1","mystery_binding":42},{"pane_call_id":"pane_2","provider_call_id":"call_2","provider_item_id":"fc_2"}],"items":[{"type":"reasoning","id":"rs_1","status":"completed","summary":[],"encrypted_content":"ENC\u00e9","seq":9007199254740993,"ratio":1.5,"mystery_item":"keep"},{"type":"function_call","id":"fc_2","status":"completed","call_id":"call_2","name":"add","arguments":"{}"}]}`

func bindEnvelope(t *testing.T, intake string, source string) *Continuation {
	t.Helper()
	var tree map[string]any
	switch intake {
	case "strict":
		var err error
		tree, err = dd.DecodeStrictJSON([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
	case "forgiving":
		if err := json.Unmarshal([]byte(source), &tree); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown intake %q", intake)
	}
	continuation := &Continuation{}
	if err := continuation.UnmarshalDd(tree); err != nil {
		t.Fatal(err)
	}
	return continuation
}

func TestContinuationUnmarshalStrictIntake(t *testing.T) {
	continuation := bindEnvelope(t, "strict", testEnvelopeJSON)
	if continuation.Format != "codex-responses-items" || continuation.Version != 1 {
		t.Fatalf("named fields wrong: %+v", continuation)
	}
	if continuation.Identity.Provider != "openai-codex" || continuation.Identity.UpstreamModel != "gpt-5.6-sol" || continuation.Identity.Profile != "p1" || continuation.Identity.AccountScope != "chatgpt:ea1e30f202148af5945f8c85" {
		t.Fatalf("identity wrong: %+v", continuation.Identity)
	}
	if len(continuation.Bindings) != 2 || continuation.Bindings[0].PaneCallID != "pane_1" || continuation.Bindings[0].ProviderCallID != "call_1" || continuation.Bindings[0].ProviderItemID != "fc_1" {
		t.Fatalf("bindings wrong: %+v", continuation.Bindings)
	}
	if len(continuation.Items) != 2 {
		t.Fatalf("items wrong: %+v", continuation.Items)
	}
	first := string(continuation.Items[0])
	for _, want := range []string{`"seq":9007199254740993`, `"ratio":1.5`, `"mystery_item":"keep"`, `"encrypted_content":"ENCé"`} {
		if !strings.Contains(first, want) {
			t.Fatalf("opaque item lost %q: %s", want, first)
		}
	}
	if !strings.Contains(string(continuation.Items[1]), `"call_id":"call_2"`) {
		t.Fatalf("second item lost: %s", continuation.Items[1])
	}
}

// TestContinuationUnknownKeysDiscardedOnReemission proves the regression
// directly: unknown keys at the root, identity, and binding levels are
// accepted by the bind but absent after re-emission, while unknown keys
// inside opaque provider items survive. re-emission goes through Message,
// the only path pane serializes continuations on.
func TestContinuationUnknownKeysDiscardedOnReemission(t *testing.T) {
	continuation := bindEnvelope(t, "strict", testEnvelopeJSON)
	message := Message{Role: "assistant", Continuation: continuation}
	unbound, err := dd.Unbind(message)
	if err != nil {
		t.Fatal(err)
	}
	reemitted, err := json.Marshal(unbound)
	if err != nil {
		t.Fatal(err)
	}
	for _, discarded := range []string{"mystery_root", "mystery_identity", "mystery_binding", "null_field"} {
		if strings.Contains(string(reemitted), discarded) {
			t.Fatalf("unknown key %s re-emitted: %s", discarded, reemitted)
		}
	}
	if !strings.Contains(string(reemitted), `"mystery_item":"keep"`) {
		t.Fatalf("unknown opaque item key dropped: %s", reemitted)
	}
}

func TestContinuationUnmarshalForgivingIntake(t *testing.T) {
	continuation := bindEnvelope(t, "forgiving", testEnvelopeJSON)
	if continuation.Version != 1 {
		t.Fatalf("float64 version not normalized: %d", continuation.Version)
	}
	first := string(continuation.Items[0])
	// forgiving intake rounds the large integer before binding; the
	// rounded value is preserved, never repaired
	if !strings.Contains(first, "9007199254740992") {
		t.Fatalf("rounded value not preserved: %s", first)
	}
	if !strings.Contains(first, `"mystery_item":"keep"`) {
		t.Fatalf("unknown opaque item key dropped: %s", first)
	}
	for _, malformed := range []string{
		`{"format":"x","v":1.5}`,
		`{"format":"x","v":1e300}`,
	} {
		var tree map[string]any
		if err := json.Unmarshal([]byte(malformed), &tree); err != nil {
			t.Fatal(err)
		}
		if err := (&Continuation{}).UnmarshalDd(tree); err == nil {
			t.Fatalf("malformed version accepted: %s", malformed)
		}
	}
}

func TestContinuationUnmarshalRejections(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"string version", `{"format":"x","v":"1"}`},
		{"fractional version", `{"format":"x","v":1.5}`},
		{"boolean version", `{"format":"x","v":true}`},
		{"null version", `{"format":"x","v":null}`},
		{"object version", `{"format":"x","v":{}}`},
		{"null items", `{"format":"x","v":1,"items":null}`},
		{"string items", `{"format":"x","v":1,"items":"x"}`},
		{"number items", `{"format":"x","v":1,"items":5}`},
		{"object items", `{"format":"x","v":1,"items":{}}`},
		{"number item element", `{"format":"x","v":1,"items":[1]}`},
		{"null item element", `{"format":"x","v":1,"items":[null]}`},
		{"string item element", `{"format":"x","v":1,"items":["s"]}`},
		{"array item element", `{"format":"x","v":1,"items":[[1]]}`},
		{"string bindings", `{"format":"x","v":1,"bindings":"x"}`},
		{"non-object binding element", `{"format":"x","v":1,"bindings":[5]}`},
		{"array identity", `{"format":"x","v":1,"identity":[]}`},
		{"string identity", `{"format":"x","v":1,"identity":"x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := dd.DecodeStrictJSON([]byte(tc.json))
			if err != nil {
				t.Fatal(err)
			}
			if err := (&Continuation{}).UnmarshalDd(tree); err == nil {
				t.Fatalf("malformed envelope accepted: %s", tc.json)
			}
		})
	}
}

func TestContinuationUnmarshalAbsentFields(t *testing.T) {
	empty := bindEnvelope(t, "strict", `{}`)
	if empty.Format != "" || empty.Version != 0 || empty.Identity != (RoundIdentity{}) {
		t.Fatalf("absent fields not zero: %+v", empty)
	}
	if empty.Bindings == nil || len(empty.Bindings) != 0 {
		t.Fatalf("bindings should be non-nil empty, got %#v", empty.Bindings)
	}
	if empty.Items != nil {
		t.Fatalf("items should be nil, got %#v", empty.Items)
	}
	itemsOnly := bindEnvelope(t, "strict", `{"v":1,"items":[{"a":1}]}`)
	if itemsOnly.Version != 1 || len(itemsOnly.Items) != 1 || !strings.Contains(string(itemsOnly.Items[0]), `"a":1`) {
		t.Fatalf("items-only envelope wrong: %+v", itemsOnly)
	}
	unknownRoot := bindEnvelope(t, "strict", `{"format":"x","v":1,"mystery":true}`)
	if unknownRoot.Format != "x" || unknownRoot.Version != 1 {
		t.Fatalf("unknown root key not tolerated: %+v", unknownRoot)
	}
	nullIdentity := bindEnvelope(t, "strict", `{"format":"x","v":1,"identity":null}`)
	if nullIdentity.Identity != (RoundIdentity{}) {
		t.Fatalf("null identity should bind as absent: %+v", nullIdentity.Identity)
	}
}

func TestContinuationVersionFloatBoundaries(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("boundary values are 64-bit specific")
	}
	// through the real forgiving intake: json.Unmarshal produces float64
	// for every number, including -2^63, which is exactly representable.
	// the converter's exact half-open range [-2^63, 2^63) is the guard
	// between the representable boundary values and a wrapped conversion.
	upper := math.Nextafter(9223372036854775808.0, 0)
	lower := math.Inf(-1)
	belowLower := math.Nextafter(-9223372036854775808.0, lower)
	cases := []struct {
		name    string
		value   float64
		want    int
		wantErr bool
	}{
		{"upper bound 2^63 rejected", 9223372036854775808.0, 0, true},
		{"just below upper bound accepted", upper, int(upper), false},
		{"lower bound -2^63 accepted", -9223372036854775808.0, -9223372036854775808, false},
		{"just below lower bound rejected", belowLower, 0, true},
		{"not integral rejected", 2.5, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			literal := strconv.FormatFloat(tc.value, 'f', -1, 64)
			var data map[string]any
			if err := json.Unmarshal([]byte(`{"format":"x","v":`+literal+`}`), &data); err != nil {
				t.Fatal(err)
			}
			c := &Continuation{}
			err := c.UnmarshalDd(data)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("out-of-range version accepted as %d", c.Version)
				}
				return
			}
			if err != nil {
				t.Fatalf("in-range version rejected: %v", err)
			}
			if c.Version != tc.want {
				t.Fatalf("version = %d, want %d", c.Version, tc.want)
			}
		})
	}
	// strict intake keeps numbers as json.Number, so overflow is a
	// ParseInt failure rather than a float wrap: same rejection, exact path
	strict := map[string]any{}
	if tree, err := dd.DecodeStrictJSON([]byte(`{"format":"x","v":9223372036854775808}`)); err != nil {
		t.Fatal(err)
	} else {
		for key, value := range tree {
			strict[key] = value
		}
	}
	if err := (&Continuation{}).UnmarshalDd(strict); err == nil {
		t.Fatal("strict-intake overflow accepted")
	}
}

func TestContinuationMarshalBindSymmetry(t *testing.T) {
	continuation := &Continuation{
		Format:   "codex-responses-items",
		Version:  1,
		Identity: RoundIdentity{Provider: "openai-codex", Protocol: "responses", UpstreamModel: "gpt-5.6-sol", Service: "https://chatgpt.com/backend-api/codex/responses", AccountScope: "chatgpt:ea1e30f202148af5945f8c85"},
		Items: []json.RawMessage{
			[]byte(`{"type":"reasoning","id":"rs_1","status":"completed","summary":[],"encrypted_content":"ENC\u00e9","seq":9007199254740993,"mystery_item":"keep"}`),
			[]byte(`{"type":"function_call","id":"fc_2","status":"completed","call_id":"call_2","name":"add","arguments":"{}"}`),
		},
		Bindings: []CallBinding{{PaneCallID: "pane_1", ProviderCallID: "call_1", ProviderItemID: "fc_1"}},
	}
	message := Message{Role: "assistant", Content: StringContent("hi"), Continuation: continuation}
	unbound, err := dd.Unbind(message)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := json.Marshal(unbound)
	if err != nil {
		t.Fatal(err)
	}
	// strict intake re-encodes exact number lexemes, so the full round
	// trip must reproduce the original bytes
	tree, err := dd.DecodeStrictJSON(bytes)
	if err != nil {
		t.Fatal(err)
	}
	var restored Message
	if err := dd.Bind(&restored, tree, dd.Strict()); err != nil {
		t.Fatal(err)
	}
	if restored.Continuation == nil || restored.Continuation.Version != 1 || len(restored.Continuation.Items) != 2 {
		t.Fatalf("continuation not restored: %+v", restored.Continuation)
	}
	if restored.Continuation.Identity.AccountScope != continuation.Identity.AccountScope || restored.Continuation.Identity.UpstreamModel != continuation.Identity.UpstreamModel {
		t.Fatalf("identity drift: %+v", restored.Continuation.Identity)
	}
	if len(restored.Continuation.Bindings) != 1 || restored.Continuation.Bindings[0] != continuation.Bindings[0] {
		t.Fatalf("bindings drift: %+v", restored.Continuation.Bindings)
	}
	restoredUnbound, err := dd.Unbind(restored)
	if err != nil {
		t.Fatal(err)
	}
	reemitted, err := json.Marshal(restoredUnbound)
	if err != nil {
		t.Fatal(err)
	}
	if string(reemitted) != string(bytes) {
		t.Fatalf("marshal/bind asymmetry:\n got %s\nwant %s", reemitted, bytes)
	}
	if !strings.Contains(string(restored.Continuation.Items[0]), `"seq":9007199254740993`) || !strings.Contains(string(restored.Continuation.Items[0]), `"mystery_item":"keep"`) {
		t.Fatalf("opaque item fidelity lost: %s", restored.Continuation.Items[0])
	}
}
