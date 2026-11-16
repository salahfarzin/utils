package audit

import (
	"slices"
	"strings"
	"unicode"
)

// Redacted replaces the value of a sensitive field.
const Redacted = "[REDACTED]"

// DefaultSensitiveKeys are matched against field names word by word, case
// and separator insensitive: "password" catches password, newPassword and
// password_hash; "api_key" catches apiKey, APIKey and x-api-key. Whole words
// only, so "token" catches access_token and refreshToken but not tokens_used.
var DefaultSensitiveKeys = []string{
	"password", "passwd", "pwd",
	"secret", "token", "credential", "credentials",
	"api_key", "apikey", "authorization", "cookie",
	"private_key", "privatekey",
	"card_number", "cardnumber", "cvv", "cvc", "iban", "ssn", "otp",
}

type redactor struct {
	needles [][]string
}

func newRedactor(extra []string) *redactor {
	r := &redactor{}
	for _, k := range append(slices.Clone(DefaultSensitiveKeys), extra...) {
		if words := splitWords(k); len(words) > 0 {
			r.needles = append(r.needles, words)
		}
	}
	return r
}

// sensitiveKey reports whether a field name contains one of the needles as
// consecutive whole words.
func (r *redactor) sensitiveKey(key string) bool {
	words := splitWords(key)
	for _, needle := range r.needles {
		if containsRun(words, needle) {
			return true
		}
	}
	return false
}

// value returns a copy of v with sensitive fields redacted at any depth.
// sensitive redacts v itself (it sits under a sensitive key); null stays null.
func (r *redactor) value(v any, sensitive bool) any {
	if sensitive {
		if v == nil {
			return nil
		}
		return Redacted
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = r.value(e, r.sensitiveKey(k))
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = r.value(e, false)
		}
		return out
	}
	return v
}

func containsRun(words, run []string) bool {
	for i := 0; i+len(run) <= len(words); i++ {
		match := true
		for j := range run {
			if words[i+j] != run[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// splitWords lower-cases a field name and splits it on separators and
// camelCase boundaries, keeping acronyms together: "XAPIKey_v2" →
// [x api key v2] ("XAPI" splits only where a lower-case letter follows).
func splitWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, c := range rs {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) {
			flush()
			continue
		}
		if unicode.IsUpper(c) && len(cur) > 0 {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, c)
	}
	flush()
	return words
}
