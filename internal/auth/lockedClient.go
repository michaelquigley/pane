package auth

import (
	"errors"
	"net/http"
	"net/url"
	"time"
)

const GenerationURL = "https://chatgpt.com/backend-api/codex/responses"

const authRequestTimeout = 15 * time.Second

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type LockedClient struct {
	client  *http.Client
	allowed map[string]bool
}

func (c *LockedClient) Do(req *http.Request) (*http.Response, error) {
	if req == nil || !allowedURL(req.URL, c.allowed) {
		return nil, errors.New("credential destination refused")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errors.New("credential request failed")
	}
	return resp, nil
}

func allowedURL(u *url.URL, set map[string]bool) bool {
	return u != nil && u.User == nil && u.RawQuery == "" && u.Fragment == "" && set[u.Scheme+"://"+u.Host+u.Path]
}

func lockedClient(base http.RoundTripper, allowed ...string) *LockedClient {
	return newLockedClient(base, authRequestTimeout, allowed...)
}

func newLockedClient(base http.RoundTripper, timeout time.Duration, allowed ...string) *LockedClient {
	if base == nil {
		base = http.DefaultTransport
	}
	set := make(map[string]bool, len(allowed))
	for _, target := range allowed {
		set[target] = true
	}
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if !allowedURL(r.URL, set) {
				return nil, errors.New("credential destination refused")
			}
			return base.RoundTrip(r)
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("credential redirect refused") },
		Timeout:       timeout,
	}
	return &LockedClient{client: client, allowed: set}
}

// the generation client confines subscription bearer requests to the fixed route.
func NewGenerationClient(base http.RoundTripper) *LockedClient {
	return newLockedClient(base, 0, GenerationURL)
}
