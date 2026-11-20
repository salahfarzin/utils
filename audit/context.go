package audit

import (
	"context"
	"net/url"
	"strings"
	"sync"

	"github.com/salahfarzin/utils/middlewares"
)

// requestState is what the interceptor/middleware knows about the current
// request. It's a pointer in the context, so handlers deeper in the chain
// (which get derived contexts) and the interceptor share it.
type requestState struct {
	mu        sync.Mutex
	ip        string
	userAgent string
	referer   string // already cleaned (cleanReferer)
	requestID string
	user      *middlewares.User
	recorded  bool
}

type stateKey struct{}

func withState(ctx context.Context, st *requestState) context.Context {
	return context.WithValue(ctx, stateKey{}, st)
}

func stateFrom(ctx context.Context) *requestState {
	st, _ := ctx.Value(stateKey{}).(*requestState)
	return st
}

func (s *requestState) setUser(u *middlewares.User) {
	s.mu.Lock()
	s.user = u
	s.mu.Unlock()
}

func (s *requestState) markRecorded() {
	s.mu.Lock()
	s.recorded = true
	s.mu.Unlock()
}

func (s *requestState) getReferer() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.referer
}

// cleanReferer keeps a Referer's scheme, host and path. Its query and
// fragment can carry one-time secrets (password-reset and magic-link tokens,
// OTP links, OAuth code and state) that don't belong in a long-kept audit
// log, and so can a user:password@ in the host part. Anything that isn't an
// http(s) URL is dropped; a very long path is cut.
func cleanReferer(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	clean := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawPath: u.RawPath}).String()
	const maxLen = 512
	if len(clean) > maxLen {
		clean = clean[:maxLen]
	}
	return clean
}

func (s *requestState) snapshot() (ip, userAgent, requestID string, user *middlewares.User, recorded bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ip, s.userAgent, s.requestID, s.user, s.recorded
}
