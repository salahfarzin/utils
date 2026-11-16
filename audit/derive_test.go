package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDeriveDiffsAndRedactsLikeARecorder(t *testing.T) {
	d, err := Derive(
		json.RawMessage(`{"price": 10, "api_key": "old", "variants": [{"id": "v1", "stock": 3}]}`),
		json.RawMessage(`{"price": 12, "api_key": "new", "variants": [{"id": "v1", "stock": 5}]}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]Change{}
	for _, c := range d.Changes {
		paths[c.Path] = c
	}
	if len(d.Changes) != 3 {
		t.Fatalf("changes = %+v", d.Changes)
	}
	if _, ok := paths["/variants/id=v1/stock"]; !ok {
		t.Fatalf("nested change by identity missing: %+v", d.Changes)
	}
	if paths["/api_key"].From != Redacted || paths["/api_key"].To != Redacted {
		t.Fatalf("a secret's values leaked: %+v", paths["/api_key"])
	}
	if strings.Contains(string(d.Before), "old") || strings.Contains(string(d.After), `"new"`) {
		t.Fatalf("snapshots not redacted: %s %s", d.Before, d.After)
	}
	if d.Truncated {
		t.Fatal("small states truncated")
	}
}

func TestDeriveWithOneState(t *testing.T) {
	d, err := Derive(nil, json.RawMessage(`{"password": "p", "name": "n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Changes != nil || d.Before != nil {
		t.Fatalf("a creation has no changes or before: %+v", d)
	}
	if !strings.Contains(string(d.After), Redacted) {
		t.Fatalf("after not redacted: %s", d.After)
	}
	d, err = Derive(json.RawMessage(`null`), nil)
	if err != nil || d.Before != nil || d.After != nil {
		t.Fatalf("null states = %+v, %v", d, err)
	}
	if _, err := Derive(json.RawMessage(`{`), nil); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestDeriveTruncatesLargeStates(t *testing.T) {
	big := json.RawMessage(`{"text": "` + strings.Repeat("x", DefaultMaxSnapshotBytes) + `"}`)
	d, err := Derive(json.RawMessage(`{"text": "a"}`), big)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Truncated || d.Before != nil || d.After != nil || len(d.Changes) != 1 {
		t.Fatalf("large state = truncated %v, before %d, after %d, changes %d", d.Truncated, len(d.Before), len(d.After), len(d.Changes))
	}
}
