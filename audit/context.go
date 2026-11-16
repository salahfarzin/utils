package audit

import (
	"context"
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

func (s *requestState) snapshot() (ip, userAgent, requestID string, user *middlewares.User, recorded bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ip, s.userAgent, s.requestID, s.user, s.recorded
}
