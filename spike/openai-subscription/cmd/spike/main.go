// spike is the operator-facing harness for the openai subscription
// verification spike. every command that reaches the network requires an
// approved budget ledger; nothing here retries automatically.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"net/http"

	"github.com/michaelquigley/pane/spike/openai-subscription/auth"
	"github.com/michaelquigley/pane/spike/openai-subscription/budget"
	"github.com/michaelquigley/pane/spike/openai-subscription/codex"
	"github.com/michaelquigley/pane/spike/openai-subscription/convo"
	"github.com/michaelquigley/pane/spike/openai-subscription/loop"
	"github.com/michaelquigley/pane/spike/openai-subscription/mcpfix"
	"github.com/michaelquigley/pane/spike/openai-subscription/qwen"
	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

func usage() {
	fmt.Fprint(os.Stderr, `usage: spike <command> [flags]

commands:
  budget-init   create the approved budget ledger (refuses to overwrite)
  budget        show the ledger
  login         sign in (--method browser|device) into the private scratch store
  status        show safe credential status
  logout        remove local scratch credentials (revokes nothing upstream)
  turn          append --prompt to --doc and run one turn on --conn
  round         run exactly one adapter round on --doc (no tool execution)
  seed          write a synthetic document (--kind forced-final); no network

state lives under --state (default ~/.local/state/pane-spike); everything
there is private (0700/0600).
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "budget-init":
		err = cmdBudgetInit(args)
	case "budget":
		err = cmdBudget(args)
	case "login":
		err = cmdLogin(args)
	case "status":
		err = cmdStatus(args)
	case "logout":
		err = cmdLogout(args)
	case "turn":
		err = cmdTurn(args, false)
	case "round":
		err = cmdTurn(args, true)
	case "seed":
		err = cmdSeed(args)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func defaultState() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "pane-spike")
}

type common struct {
	state      string
	ledgerName string
}

func (c *common) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.state, "state", defaultState(), "private state directory")
	fs.StringVar(&c.ledgerName, "ledger", "budget.json", "ledger file name under --state (one per authorized budget)")
}

func (c *common) ensure() error {
	if err := os.MkdirAll(c.state, 0o700); err != nil {
		return err
	}
	return os.Chmod(c.state, 0o700)
}
func (c *common) ledger() string   { return filepath.Join(c.state, c.ledgerName) }
func (c *common) authDir() string  { return filepath.Join(c.state, "auth") }
func (c *common) captures() string { return filepath.Join(c.state, "captures") }

func cmdBudgetInit(args []string) error {
	fs := flag.NewFlagSet("budget-init", flag.ExitOnError)
	var c common
	c.bind(fs)
	total := fs.Int("total", 0, "approved total upstream requests")
	per := fs.String("per", "", "per-route ceilings, e.g. sol=6,astra=4,qwen-eleven=6,qwen-fortyfive=6,auth=4")
	wall := fs.Duration("wall", 0, "approved total wall-clock window from first spend")
	_ = fs.Parse(args)
	if *total <= 0 || *per == "" || *wall <= 0 {
		return errors.New("--total, --per and --wall are required")
	}
	perRoute := map[string]int{}
	for _, kv := range strings.Split(*per, ",") {
		k, v, ok := strings.Cut(kv, "=")
		n, err := strconv.Atoi(v)
		if !ok || err != nil || n < 0 {
			return fmt.Errorf("bad --per entry '%s'", kv)
		}
		perRoute[k] = n
	}
	if err := c.ensure(); err != nil {
		return err
	}
	return budget.InitLedger(c.ledger(), *total, perRoute, *wall)
}

func cmdBudget(args []string) error {
	fs := flag.NewFlagSet("budget", flag.ExitOnError)
	var c common
	c.bind(fs)
	_ = fs.Parse(args)
	l, err := budget.ReadLedger(c.ledger())
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(l, "", "  ")
	fmt.Println(string(b))
	return nil
}

func manager(c *common) (*auth.Manager, error) {
	store, err := auth.OpenStore(c.authDir())
	if err != nil {
		return nil, err
	}
	// refresh traffic counts against the 'auth' route.
	return auth.NewManager(store, budget.LedgerTransport(c.ledger(), "auth", "oauth", nil)), nil
}

func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	var c common
	c.bind(fs)
	method := fs.String("method", "device", "browser|device")
	originator := fs.String("originator", "pane", "authorize originator parameter")
	_ = fs.Parse(args)
	if err := c.ensure(); err != nil {
		return err
	}
	// login traffic (authorize, device polling, code exchange) is not
	// generation and is bounded by the device-code window, not the ledger.
	store, err := auth.OpenStore(c.authDir())
	if err != nil {
		return err
	}
	m := auth.NewManager(store, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	show := func(format string, a ...any) { fmt.Fprintf(os.Stderr, format, a...) }
	switch *method {
	case "browser":
		err = m.LoginBrowser(ctx, *originator, show)
	case "device":
		err = m.LoginDevice(ctx, show)
	default:
		return fmt.Errorf("unknown method '%s'", *method)
	}
	if err != nil {
		return err
	}
	return printStatus(m)
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	var c common
	c.bind(fs)
	_ = fs.Parse(args)
	m, err := manager(&c)
	if err != nil {
		return err
	}
	return printStatus(m)
}

func printStatus(m *auth.Manager) error {
	b, _ := json.MarshalIndent(m.Status(), "", "  ")
	fmt.Println(string(b))
	return nil
}

func cmdLogout(args []string) error {
	fs := flag.NewFlagSet("logout", flag.ExitOnError)
	var c common
	c.bind(fs)
	_ = fs.Parse(args)
	store, err := auth.OpenStore(c.authDir())
	if err != nil {
		return err
	}
	return store.Logout(context.Background())
}

// capture writes raw payloads to a private file and keeps a value-free
// shape summary (event types and key names) for the report.
type capture struct {
	raw    *os.File
	shapes []string
}

func openCapture(c *common, label string) (*capture, error) {
	if err := os.MkdirAll(c.captures(), 0o700); err != nil {
		return nil, err
	}
	name := time.Now().UTC().Format("20060102T150405Z") + "-" + label
	f, err := os.OpenFile(filepath.Join(c.captures(), name+".raw.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &capture{raw: f}, nil
}

func (cp *capture) trace(payload []byte) {
	cp.raw.Write(append(append([]byte(nil), payload...), '\n'))
	var m map[string]any
	if json.Unmarshal(payload, &m) != nil {
		cp.shapes = append(cp.shapes, string(payload)) // e.g. [DONE]
		return
	}
	cp.shapes = append(cp.shapes, shape(m))
}

// shape renders keys recursively without values.
func shape(v any) string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			if k == "type" {
				parts = append(parts, fmt.Sprintf("type=%v", t[k]))
				continue
			}
			parts = append(parts, k+":"+shape(t[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		return "[" + shape(t[0]) + fmt.Sprintf("x%d]", len(t))
	case string:
		return "s"
	case float64:
		return "n"
	case bool:
		return "b"
	case nil:
		return "null"
	}
	return "?"
}

func (cp *capture) close(summaryPath string, extra map[string]any) error {
	cp.raw.Close()
	extra["event_shapes"] = cp.shapes
	b, _ := json.MarshalIndent(extra, "", "  ")
	return os.WriteFile(summaryPath, b, 0o600)
}

// perRound bounds each upstream round with its own deadline.
type perRound struct {
	round.Adapter
	timeout time.Duration
}

func (p perRound) Round(ctx context.Context, req round.Request, emit func(round.Event)) (*round.Final, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return p.Adapter.Round(ctx, req, emit)
}

func cmdTurn(args []string, single bool) error {
	fs := flag.NewFlagSet("turn", flag.ExitOnError)
	var c common
	c.bind(fs)
	conn := fs.String("conn", "", "sol|astra|qwen-eleven|qwen-fortyfive")
	effort := fs.String("effort", "", "reasoning_effort (omit for upstream default)")
	docPath := fs.String("doc", "", "conversation document (created if missing)")
	prompt := fs.String("prompt", "", "user message to append")
	system := fs.String("system", "you are a terse assistant.", "system prompt for a new document; empty for none")
	tools := fs.Bool("tools", false, "offer the mcpadd fixture tool")
	deny := fs.Bool("deny", false, "deny every tool call")
	approveOnce := fs.Bool("approve-once", false, "approve only the first tool call of the turn; deny the rest")
	captureRequests := fs.Bool("capture-requests", false, "save each outgoing generation body privately (0600) beside the capture")
	forced := fs.Bool("forced-final", false, "with 'round': forced-final intent (tools withheld)")
	maxRounds := fs.Int("max-rounds", 2, "round ceiling for this turn")
	maxTokens := fs.Int("max-tokens", 2048, "qwen max_tokens")
	maxOutput := fs.Int("max-output-tokens", 0, "subscription max_output_tokens acceptance probe (0 = omit)")
	fixture := fs.String("fixture", "", "path to the built mcpadd fixture")
	endpoint := fs.String("endpoint", "", "qwen endpoint override (defaults per connection)")
	keyFile := fs.String("qwen-key-file", "", "0600 file holding the qwen bearer key (default <state>/qwen.key)")
	requestTimeout := fs.Duration("request-timeout", 3*time.Minute, "per-round wall-clock ceiling")
	totalTimeout := fs.Duration("timeout", 10*time.Minute, "whole-invocation ceiling")
	_ = fs.Parse(args)
	if *conn == "" || *docPath == "" {
		return errors.New("--conn and --doc are required")
	}
	if err := c.ensure(); err != nil {
		return err
	}
	if *keyFile == "" {
		*keyFile = filepath.Join(c.state, "qwen.key")
	}
	if _, err := budget.ReadLedger(c.ledger()); err != nil {
		return fmt.Errorf("no approved budget ledger at '%s'; run budget-init with the approved ceilings", c.ledger())
	}

	cp, err := openCapture(&c, *conn)
	if err != nil {
		return err
	}
	var adapter round.Adapter
	var codexAdapter *codex.Adapter
	switch *conn {
	case "sol", "astra":
		model := map[string]string{"sol": "gpt-5.6-sol", "astra": "gpt-6-astra"}[*conn]
		m, err := manager(&c)
		if err != nil {
			return err
		}
		var base http.RoundTripper
		if *captureRequests {
			base = cp.requestRecorder()
		}
		ca, err := codex.New(codex.Config{Alias: model, UpstreamModel: model, Effort: *effort, MaxOutputTokens: *maxOutput, Trace: cp.trace},
			m, budget.LedgerTransport(c.ledger(), *conn, "generation", base))
		if err != nil {
			return err
		}
		codexAdapter = ca
		adapter = ca
		if err != nil {
			return err
		}
	case "qwen-eleven", "qwen-fortyfive":
		if *maxOutput != 0 {
			return errors.New("--max-output-tokens applies to subscription connections only")
		}
		key, err := readKey(*keyFile)
		if err != nil {
			return err
		}
		ep, profile, alias := "http://eleven:11400/v1", "qwen3.8-ninfer", "qwen3.8-27b@eleven"
		if *conn == "qwen-fortyfive" {
			ep, profile, alias = "http://fortyfive:11400/v1", "qwen3.8-llamacpp", "qwen3.8-27b@fortyfive"
		}
		if *endpoint != "" {
			ep = *endpoint
		}
		adapter, err = qwen.New(qwen.Config{Alias: alias, Endpoint: ep, APIKey: key, UpstreamModel: "qwen3.8-27b", Profile: profile, Effort: *effort, MaxTokens: *maxTokens, Trace: cp.trace},
			budget.LedgerTransport(c.ledger(), *conn, "generation", nil))
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown connection '%s'", *conn)
	}

	doc, err := convo.Load(*docPath)
	if errors.Is(err, os.ErrNotExist) {
		doc = &convo.Document{Title: filepath.Base(*docPath)}
		if *system != "" {
			doc.Messages = append(doc.Messages, round.Message{Role: "system", Content: round.Str(*system)})
		}
	} else if err != nil {
		return err
	}
	if *prompt != "" {
		doc.Messages = append(doc.Messages, round.Message{Role: "user", Content: round.Str(*prompt)})
	}

	ctx, cancel := context.WithTimeout(context.Background(), *totalTimeout)
	defer cancel()
	summary := map[string]any{"conn": *conn, "effort": *effort, "single_round": single}
	var events []string
	onEvent := func(e round.Event) {
		if len(events) == 0 || events[len(events)-1] != string(e.Kind) {
			events = append(events, string(e.Kind))
		}
	}
	if codexAdapter != nil {
		// replay decisions for the history as it will first be sent.
		_, decisions, err := codexAdapter.BuildBody(round.Request{History: doc.Messages}, codexAdapter.Identity())
		if err != nil {
			summary["build_error"] = err.Error()
		}
		summary["replay_decisions"] = decisions
	}
	a := perRound{Adapter: adapter, timeout: *requestTimeout}

	var runErr error
	if single {
		intent := round.IntentNormal
		if *forced {
			intent = round.IntentForcedFinal
		}
		final, err := a.Round(ctx, round.Request{History: doc.Messages, Intent: intent}, onEvent)
		runErr = err
		if final != nil {
			summary["finish"], summary["raw_finish"], summary["usage"] = final.Finish, final.RawFinish, final.Usage
			summary["tool_calls_requested"] = len(final.Assistant.ToolCalls)
			if len(final.Assistant.ToolCalls) == 0 {
				doc.Messages = append(doc.Messages, final.Assistant)
			}
		}
	} else {
		var execClient *mcpfix.Client
		var offered []round.Tool
		if *tools {
			if *fixture == "" {
				return errors.New("--tools requires --fixture")
			}
			execLog := filepath.Join(c.state, "fixture-exec.log")
			execClient, err = mcpfix.Start(ctx, *fixture, []string{"PANE_SPIKE_EXEC_LOG=" + execLog})
			if err != nil {
				return err
			}
			defer execClient.Close()
			offered = execClient.Tools()
		}
		var approver loop.Approver = loop.ApproveAll{}
		if *deny {
			approver = loop.DenyAll{}
		} else if *approveOnce {
			approver = &approveOnceApprover{}
		}
		rec := convo.NewFileRecorder(*docPath, doc)
		var executor loop.Executor
		if execClient != nil {
			executor = execClient
		}
		res, err := loop.RunTurn(ctx, a, doc.Messages, offered, executor, approver, rec, loop.Options{MaxRounds: *maxRounds, OnEvent: onEvent,
			OnRoundComplete: func(f *round.Final, _ []round.Message) {
				rounds, _ := summary["rounds"].([]any)
				summary["rounds"] = append(rounds, map[string]any{"finish": f.Finish, "raw_finish": f.RawFinish, "usage": f.Usage, "tool_calls": len(f.Assistant.ToolCalls), "has_continuation": f.Assistant.Continuation != nil})
			}})
		runErr = err
		if res != nil {
			doc.Messages = append(doc.Messages, res.Messages...)
			summary["record_state"], summary["recovery"] = res.Record.State, loop.Assess(res.Record)
		}
	}
	summary["events"] = events
	if runErr != nil {
		summary["error_kind"], summary["error"] = round.KindOf(runErr), runErr.Error()
	}
	if err := convo.Save(*docPath, doc); err != nil {
		return err
	}
	summaryPath := strings.TrimSuffix(cp.raw.Name(), ".raw.jsonl") + ".summary.json"
	if err := cp.close(summaryPath, summary); err != nil {
		return err
	}
	fmt.Println(summaryPath)
	return runErr
}

// readKey loads a bearer key from an owner-only file so it never appears in
// command arguments.
func readKey(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("qwen key file: %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("qwen key file '%s' must not be group/world readable", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return "", errors.New("qwen key file is empty")
	}
	return key, nil
}

// cmdSeed writes synthetic history for the forced-final recovery check: two
// failed calls of the same tool, as pane's loop would have recorded them.
func cmdSeed(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	kind := fs.String("kind", "forced-final", "forced-final")
	docPath := fs.String("doc", "", "document to create (must not exist)")
	_ = fs.Parse(args)
	if *kind != "forced-final" || *docPath == "" {
		return errors.New("--kind forced-final and --doc are required")
	}
	if _, err := os.Stat(*docPath); err == nil {
		return fmt.Errorf("'%s' exists", *docPath)
	}
	bad := `{"a":"seventeen","b":25}`
	failure := "error: a must be an integer"
	doc := &convo.Document{Title: "forced-final seed", Messages: []round.Message{
		{Role: "system", Content: round.Str("you are a terse assistant.")},
		{Role: "user", Content: round.Str("use the add tool to add seventeen and 25, then reply with only the sum.")},
		{Role: "assistant", ToolCalls: []round.ToolCall{{ID: "pane_call_seed_1", Name: "add", Arguments: bad}}},
		{Role: "tool", ToolCallID: "pane_call_seed_1", Content: round.Str(failure)},
		{Role: "assistant", ToolCalls: []round.ToolCall{{ID: "pane_call_seed_2", Name: "add", Arguments: bad}}},
		{Role: "tool", ToolCallID: "pane_call_seed_2", Content: round.Str(failure)},
	}}
	return convo.Save(*docPath, doc)
}

// approveOnceApprover approves the first call of a turn and denies the rest.
type approveOnceApprover struct{ used bool }

func (a *approveOnceApprover) Approve(context.Context, round.ToolCall) (bool, error) {
	if a.used {
		return false, nil
	}
	a.used = true
	return true, nil
}

// requestRecorder saves each POST body to a private numbered file. bodies
// carry account-bound continuation and never leave the state directory.
func (cp *capture) requestRecorder() http.RoundTripper {
	n := 0
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && r.Body != nil {
			body, err := io.ReadAll(r.Body)
			r.Body.Close()
			if err != nil {
				return nil, err
			}
			n++
			name := strings.TrimSuffix(cp.raw.Name(), ".raw.jsonl") + fmt.Sprintf(".request-%d.json", n)
			if err := os.WriteFile(name, body, 0o600); err != nil {
				return nil, err
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
		}
		return http.DefaultTransport.RoundTrip(r)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
