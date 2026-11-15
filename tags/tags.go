// Package tags is the tagging every service shares (docs/tags.md is the
// contract). A tag is general (everyone's, managed by who holds Resource's
// edit grant) or one user's own; a record carries any of the general tags and
// of its owner's. A service keeps its tags next to its records, in two tables
// (see Store), so it can filter and sort by them in its own queries.
package tags

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	// Resource is auth's resource for tags: edit manages the general ones
	// (in every service). A user's own tags need no grant.
	Resource = "tags"
	// MaxNameLength is the longest name, in characters.
	MaxNameLength = 50
	// MaxPerRecord is how many tags one record may carry.
	MaxPerRecord = 20
)

var (
	ErrInvalidName = errors.New("a tag's name must be 1 to 50 characters")
	ErrTooMany     = errors.New("a record takes at most 20 tags")
	ErrExists      = errors.New("a tag with this name already exists")
	ErrNotFound    = errors.New("tag not found")
	ErrForbidden   = errors.New("managing general tags needs the tags permission")
)

// Tag is a tag as a service hands it out.
type Tag struct {
	UUID string
	Name string
	// General is everyone's; otherwise it is the caller's own.
	General bool
	// Count is how many of the caller's records carry it (Store.Catalog only).
	Count int64
}

// Normalize is name as stored: trimmed, inner whitespace collapsed to one
// space, a leading # dropped. Case is kept; names are unique ignoring it.
func Normalize(name string) (string, error) {
	name = strings.Join(strings.Fields(strings.TrimPrefix(strings.TrimSpace(name), "#")), " ")
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return "", ErrInvalidName
	}
	return name, nil
}

// normalizeAll normalizes names, dropping the ones that repeat another
// ignoring case; ErrTooMany past MaxPerRecord.
func normalizeAll(names []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, n := range names {
		name, err := Normalize(n)
		if err != nil {
			return nil, err
		}
		if key := strings.ToLower(name); !seen[key] {
			seen[key] = true
			out = append(out, name)
		}
	}
	if len(out) > MaxPerRecord {
		return nil, ErrTooMany
	}
	return out, nil
}

// Split reads a comma-separated list of names (a list request's `tags`),
// dropping empty ones.
func Split(list string) []string {
	var out []string
	for _, n := range strings.Split(list, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// Match is how a list filters on several tags: records with any of them, or
// with all.
type Match string

const (
	MatchAny Match = "any"
	MatchAll Match = "all"
)

// ParseMatch reads a list request's `tags_match`; any unless "all".
func ParseMatch(s string) Match {
	if Match(s) == MatchAll {
		return MatchAll
	}
	return MatchAny
}
