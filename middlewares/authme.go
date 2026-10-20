package middlewares

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrUnauthenticated is returned by NewAuthMeFunc when auth rejects the token.
var ErrUnauthenticated = errors.New("unauthenticated")

// meResponse is auth's /auth/me body as the REST gateway encodes it: int64 as a
// string, StringValue wrappers as bare strings, grants as {"actions": [...]}.
type meResponse struct {
	ID          json.Number                           `json:"id"`
	UUID        string                                `json:"uuid"`
	Email       wrappedString                         `json:"email"`
	Roles       []string                              `json:"roles"`
	Permissions map[string]struct{ Actions []string } `json:"permissions"`
	Mobile      wrappedString                         `json:"mobile"`
	FirstName   wrappedString                         `json:"first_name"`
	LastName    wrappedString                         `json:"last_name"`
	Gender      wrappedString                         `json:"gender"`
	Birthdate   wrappedString                         `json:"birthdate"`
}

// wrappedString reads a google.protobuf.StringValue, which the gateway sends as
// a bare string. It also accepts {"value": "..."}, the shape some hand-written
// clients expected.
type wrappedString struct{ value *string }

func (w *wrappedString) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		w.value = &s
		return nil
	}
	var obj struct{ Value string }
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	w.value = &obj.Value
	return nil
}

// NewAuthMeFunc returns an AuthServiceFunc that resolves a token by calling
// GET <authURL>/auth/me, e.g. authURL "http://auth:8082/api/v1". The user it
// returns carries auth's permissions. A rejected token is ErrUnauthenticated; a
// nil client means a 5-second timeout.
func NewAuthMeFunc(authURL string, client *http.Client) AuthServiceFunc {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	endpoint := strings.TrimRight(authURL, "/") + "/auth/me"

	return func(token string) (*User, error) {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("build /auth/me request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("auth service unreachable: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		switch {
		case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusNotFound:
			return nil, ErrUnauthenticated
		case resp.StatusCode != http.StatusOK:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("auth service: status %d: %s", resp.StatusCode, body)
		}

		var me meResponse
		if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
			return nil, fmt.Errorf("decode /auth/me: %w", err)
		}
		return me.user(), nil
	}
}

func (me meResponse) user() *User {
	u := &User{
		ID:        me.ID.String(),
		Uuid:      me.UUID,
		Roles:     me.Roles,
		Mobile:    me.Mobile.value,
		FirstName: me.FirstName.value,
		LastName:  me.LastName.value,
		Gender:    me.Gender.value,
		Birthdate: me.Birthdate.value,
	}
	if me.Email.value != nil {
		u.Email = *me.Email.value
	}
	if len(me.Permissions) > 0 {
		u.Permissions = make(Permissions, len(me.Permissions))
		for resource, g := range me.Permissions {
			u.Permissions[resource] = g.Actions
		}
	}
	return u
}
