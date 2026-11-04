package middlewares

import (
	"crypto/sha256"
	"sync"
	"time"
)

// CachedAuth wraps authenticate so that a token it accepted is trusted for ttl
// without asking again, saving a call to auth per request. Tokens are keyed by
// their SHA-256, and refusals and errors are never cached. Keep ttl short (a
// minute): a token auth revokes still works here until its entry expires.
func CachedAuth(authenticate AuthServiceFunc, ttl time.Duration) AuthServiceFunc {
	type entry struct {
		user *User
		at   time.Time
	}
	var mu sync.Mutex
	cache := map[[sha256.Size]byte]entry{}

	return func(token string) (*User, error) {
		key := sha256.Sum256([]byte(token))
		mu.Lock()
		e, ok := cache[key]
		mu.Unlock()
		if ok && time.Since(e.at) < ttl {
			return e.user, nil
		}

		user, err := authenticate(token)
		if err != nil || user == nil {
			return user, err
		}

		mu.Lock()
		for k, e := range cache {
			if time.Since(e.at) >= ttl {
				delete(cache, k)
			}
		}
		cache[key] = entry{user: user, at: time.Now()}
		mu.Unlock()
		return user, nil
	}
}
