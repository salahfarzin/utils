// Package releasenotes serves a service's release notes: its docs/changelog,
// embedded in the binary, so they are always the notes of the build that is
// running.
//
// A changelog is a folder of Markdown files: unreleased.md, what is merged
// since the last release, and one vX.Y.Z.md per release. Embed it from a
// package in that folder:
//
//	// docs/changelog/changelog.go
//	package changelog
//
//	import "embed"
//
//	//go:embed *.md
//	var Files embed.FS
//
// and serve it next to the API:
//
//	http.Handle(releasenotes.Path, releasenotes.Handler(changelog.Files, "auth"))
//
// Path is outside /api/v1, which nginx doesn't route: the notes are for the
// services on the internal network (content-manager gathers them for the
// What's new editor), not for the public.
package releasenotes

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Path is where Handler is served.
const Path = "/release-notes"

const unreleasedFile = "unreleased.md"

var (
	versionFile = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)\.md$`)
	// An entry's section ("## Features"): unreleased.md has none until
	// something is merged, only its heading and intro.
	sectionHeading = regexp.MustCompile(`(?m)^##\s+\S`)
)

// Release is one release's notes.
type Release struct {
	Version string `json:"version"`
	Notes   string `json:"notes"`
}

// Notes are a service's release notes.
type Notes struct {
	Service string `json:"service"`
	// What is merged since the last release; empty when nothing is.
	Unreleased string `json:"unreleased,omitempty"`
	// The newest release; nil before the first.
	Latest *Release `json:"latest,omitempty"`
}

// Read reads service's notes from a changelog folder.
func Read(fsys fs.FS, service string) (Notes, error) {
	notes := Notes{Service: service}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return notes, fmt.Errorf("release notes: %w", err)
	}
	if notes.Unreleased, err = readUnreleased(fsys, entries); err != nil {
		return notes, err
	}
	notes.Latest, err = readLatest(fsys, entries)
	return notes, err
}

// readUnreleased is unreleased.md, when it has entries.
func readUnreleased(fsys fs.FS, entries []fs.DirEntry) (string, error) {
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() != unreleasedFile {
			continue
		}
		text, err := readText(fsys, unreleasedFile)
		if err != nil || !sectionHeading.MatchString(text) {
			return "", err
		}
		return text, nil
	}
	return "", nil
}

// readLatest is the newest vX.Y.Z.md, by version; nil when there is none.
func readLatest(fsys fs.FS, entries []fs.DirEntry) (*Release, error) {
	var latest string
	var latestVersion [3]int
	for _, entry := range entries {
		version, ok := parseVersion(entry.Name())
		if ok && !entry.IsDir() && (latest == "" || newer(version, latestVersion)) {
			latest, latestVersion = entry.Name(), version
		}
	}
	if latest == "" {
		return nil, nil
	}
	text, err := readText(fsys, latest)
	if err != nil {
		return nil, err
	}
	return &Release{Version: strings.TrimSuffix(latest, path.Ext(latest)), Notes: text}, nil
}

// Handler serves service's notes as JSON (Notes) on GET. They are read once,
// on the first request: they're embedded, so they never change.
func Handler(fsys fs.FS, service string) http.Handler {
	var (
		once sync.Once
		body []byte
		err  error
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		once.Do(func() {
			var notes Notes
			if notes, err = Read(fsys, service); err == nil {
				body, err = json.Marshal(notes)
			}
		})
		if err != nil {
			http.Error(w, "release notes unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	})
}

func readText(fsys fs.FS, name string) (string, error) {
	content, err := fs.ReadFile(fsys, name)
	if err != nil {
		return "", fmt.Errorf("release notes: %w", err)
	}
	return strings.TrimSpace(string(content)), nil
}

func parseVersion(name string) ([3]int, bool) {
	var version [3]int
	match := versionFile.FindStringSubmatch(name)
	if match == nil {
		return version, false
	}
	for i := range version {
		n, err := strconv.Atoi(match[i+1])
		if err != nil {
			return version, false
		}
		version[i] = n
	}
	return version, true
}

func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
