package audit

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitWords(t *testing.T) {
	for in, want := range map[string][]string{
		"password":      {"password"},
		"newPassword":   {"new", "password"},
		"password_hash": {"password", "hash"},
		"APIKey":        {"api", "key"},
		"x-api-key":     {"x", "api", "key"},
		"XAPIKey_v2":    {"xapi", "key", "v2"},
		"refreshToken":  {"refresh", "token"},
		"ID":            {"id"},
	} {
		assert.Equal(t, want, splitWords(in), in)
	}
}

func TestSensitiveKey(t *testing.T) {
	r := newRedactor([]string{"national_id"})
	for _, k := range []string{
		"password", "Password", "newPassword", "password_hash", "access_token", "refreshToken",
		"client_secret", "apiKey", "APIKey", "x-api-key", "apikey", "Authorization", "cookie",
		"private_key", "card_number", "cardNumber", "cvv", "iban", "otp", "credentials",
		"nationalId", // added via the constructor
	} {
		assert.True(t, r.sensitiveKey(k), k)
	}
	for _, k := range []string{
		"tokens_used", "max_tokens", "secretary", "author", "monkey", "keyboard", "name", "email",
		"passport", "cookies_accepted",
	} {
		assert.False(t, r.sensitiveKey(k), k)
	}
}

func TestDiffRedactsButStillShowsTheChange(t *testing.T) {
	changes, err := Diff(
		map[string]any{"email": "a@x", "password": "old", "profile": map[string]any{"api_key": "k1"}},
		map[string]any{"email": "a@x", "password": "new", "profile": map[string]any{"api_key": "k2"}},
	)
	require.NoError(t, err)
	assert.Equal(t, []Change{
		{Op: OpReplace, Path: "/password", From: Redacted, To: Redacted},
		{Op: OpReplace, Path: "/profile/api_key", From: Redacted, To: Redacted},
	}, changes)
}

func TestDiffRedactsInsideAddedObjects(t *testing.T) {
	changes, err := Diff(
		map[string]any{},
		map[string]any{"integration": map[string]any{"url": "https://x", "secret": "s"}, "credentials": map[string]any{"user": "u"}},
	)
	require.NoError(t, err)
	assert.Equal(t, []Change{
		{Op: OpAdd, Path: "/credentials", To: Redacted},
		{Op: OpAdd, Path: "/integration", To: map[string]any{"url": "https://x", "secret": Redacted}},
	}, changes)
}

func TestRedactValueKeepsNulls(t *testing.T) {
	v, err := normalize(json.RawMessage(`{"token": null, "list": [{"password": "p"}]}`))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"token": nil, "list": []any{map[string]any{"password": Redacted}}},
		newRedactor(nil).value(v, false))
}
