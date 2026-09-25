package budget

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

func TestLedgerCeilings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.json")
	if err := InitLedger(path, 3, map[string]int{"sol": 2, "astra": 2}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := InitLedger(path, 99, map[string]int{"sol": 99}, time.Hour); err == nil {
		t.Fatal("ledger overwritten")
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	sol := &http.Client{Transport: LedgerTransport(path, "sol", "t", nil)}
	astra := &http.Client{Transport: LedgerTransport(path, "astra", "t", nil)}
	qwen := &http.Client{Transport: LedgerTransport(path, "qwen-eleven", "t", nil)}

	post := func(c *http.Client) error {
		resp, err := c.Post(srv.URL, "application/json", nil)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if post(sol) != nil || post(sol) != nil {
		t.Fatal("within ceiling refused")
	}
	if err := post(sol); round.KindOf(err) != round.ErrBudget {
		t.Fatalf("route ceiling not enforced: %v", err)
	}
	if err := post(qwen); round.KindOf(err) != round.ErrBudget {
		t.Fatalf("unapproved route allowed: %v", err)
	}
	if post(astra) != nil {
		t.Fatal("astra refused")
	}
	if err := post(astra); round.KindOf(err) != round.ErrBudget {
		t.Fatalf("total ceiling not enforced: %v", err)
	}
	if hits.Load() != 3 {
		t.Fatalf("%d requests reached the server", hits.Load())
	}
	l, _ := ReadLedger(path)
	if l.UsedTotal != 3 || len(l.Log) != 3 {
		t.Fatalf("ledger %+v", l)
	}
}

func TestLedgerWallClock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.json")
	_ = InitLedger(path, 5, map[string]int{"sol": 5}, time.Millisecond)
	if err := Spend(path, "sol", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := Spend(path, "sol", ""); round.KindOf(err) != round.ErrBudget {
		t.Fatalf("wall clock not enforced: %v", err)
	}
}
