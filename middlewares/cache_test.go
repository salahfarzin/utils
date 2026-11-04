package middlewares

import (
	"errors"
	"testing"
	"time"
)

func TestCachedAuth(t *testing.T) {
	calls := 0
	auth := CachedAuth(func(token string) (*User, error) {
		calls++
		if token != "good" {
			return nil, errors.New("invalid")
		}
		return &User{Uuid: "u-1"}, nil
	}, 50*time.Millisecond)

	for i := range 3 {
		if u, err := auth("good"); err != nil || u.Uuid != "u-1" {
			t.Fatalf("call %d: %+v %v", i, u, err)
		}
	}
	if calls != 1 {
		t.Errorf("an accepted token was asked %d times, want 1", calls)
	}

	for range 2 {
		if _, err := auth("bad"); err == nil {
			t.Fatal("a refused token was accepted")
		}
	}
	if calls != 3 {
		t.Errorf("refusals were cached: %d calls, want 3", calls)
	}

	time.Sleep(60 * time.Millisecond)
	if _, err := auth("good"); err != nil || calls != 4 {
		t.Errorf("an expired entry was trusted: %v, %d calls", err, calls)
	}
}
