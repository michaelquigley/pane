package budget

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

// Ledger is the approved run budget persisted across harness processes, so
// the ceiling holds even when exercises are split into fresh processes.
type Ledger struct {
	Total      int            `json:"total"`
	PerRoute   map[string]int `json:"per_route"`
	WallClock  time.Duration  `json:"wall_clock"`
	Used       map[string]int `json:"used"`
	UsedTotal  int            `json:"used_total"`
	FirstSpent time.Time      `json:"first_spent,omitzero"`
	Log        []Entry        `json:"log"`
}

// Entry records one consumed unit.
type Entry struct {
	At    time.Time `json:"at"`
	Route string    `json:"route"`
	Note  string    `json:"note,omitempty"`
}

// InitLedger creates the ledger; it refuses to overwrite an existing one.
func InitLedger(path string, total int, perRoute map[string]int, wall time.Duration) error {
	l := &Ledger{Total: total, PerRoute: perRoute, WallClock: wall, Used: map[string]int{}}
	b, _ := json.MarshalIndent(l, "", "  ")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("ledger exists or cannot be created: %w", err)
	}
	defer f.Close()
	_, err = f.Write(b)
	return err
}

// ReadLedger loads the ledger.
func ReadLedger(path string) (*Ledger, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var l Ledger
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, err
	}
	if l.Used == nil {
		l.Used = map[string]int{}
	}
	return &l, nil
}

// Spend consumes one unit under an exclusive lock, before the request goes
// out. failed and cancelled requests still count.
func Spend(path, route, note string) error {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	l, err := ReadLedger(path)
	if err != nil {
		return &round.Error{Kind: round.ErrBudget, Message: "no approved budget ledger", Err: err}
	}
	now := time.Now()
	switch {
	case l.UsedTotal >= l.Total:
		return &round.Error{Kind: round.ErrBudget, Message: fmt.Sprintf("approved total of %d requests is exhausted", l.Total)}
	case l.PerRoute[route] == 0:
		return &round.Error{Kind: round.ErrBudget, Message: fmt.Sprintf("route '%s' has no approved budget", route)}
	case l.Used[route] >= l.PerRoute[route]:
		return &round.Error{Kind: round.ErrBudget, Message: fmt.Sprintf("route '%s' approved ceiling of %d is exhausted", route, l.PerRoute[route])}
	case !l.FirstSpent.IsZero() && l.WallClock > 0 && now.Sub(l.FirstSpent) > l.WallClock:
		return &round.Error{Kind: round.ErrBudget, Message: "approved wall-clock window has elapsed"}
	}
	if l.FirstSpent.IsZero() {
		l.FirstSpent = now
	}
	l.Used[route]++
	l.UsedTotal++
	l.Log = append(l.Log, Entry{At: now, Route: route, Note: note})
	b, _ := json.MarshalIndent(l, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LedgerTransport spends one unit per POST for route.
func LedgerTransport(path, route, note string, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return rt(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			if err := Spend(path, route, note); err != nil {
				return nil, err
			}
		}
		return base.RoundTrip(r)
	})
}
