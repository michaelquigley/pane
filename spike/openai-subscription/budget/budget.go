// Package budget enforces the approved upstream request ceiling. every
// generation request -- including any retry, which the harness never makes
// automatically -- consumes one unit before it is sent.
package budget

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

// Budget counts and caps upstream requests per named route.
type Budget struct {
	mu     sync.Mutex
	limits map[string]int
	used   map[string]int
	total  int
	max    int
}

// New builds a budget with a total ceiling and optional per-route ceilings.
func New(total int, perRoute map[string]int) *Budget {
	return &Budget{limits: perRoute, used: map[string]int{}, max: total}
}

// Take consumes one unit for route or reports exhaustion.
func (b *Budget) Take(route string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.total >= b.max {
		return &round.Error{Kind: round.ErrBudget, Message: fmt.Sprintf("total request ceiling %d reached", b.max)}
	}
	if lim, ok := b.limits[route]; ok && b.used[route] >= lim {
		return &round.Error{Kind: round.ErrBudget, Message: fmt.Sprintf("route '%s' ceiling %d reached", route, lim)}
	}
	b.total++
	b.used[route]++
	return nil
}

// Used reports consumption per route and in total.
func (b *Budget) Used() (map[string]int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]int, len(b.used))
	for k, v := range b.used {
		out[k] = v
	}
	return out, b.total
}

// Transport wraps a RoundTripper so each POST consumes one unit for route.
func (b *Budget) Transport(route string, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return rt(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			if err := b.Take(route); err != nil {
				return nil, err
			}
		}
		return base.RoundTrip(r)
	})
}

type rt func(*http.Request) (*http.Response, error)

func (f rt) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
