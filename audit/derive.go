package audit

import "encoding/json"

// Derived is what a Recorder works out from a record's states, for an event
// whose producer sent only those states: a service in another language,
// which can't run this package's diff and redaction itself.
type Derived struct {
	// The states with sensitive fields redacted; nil past
	// DefaultMaxSnapshotBytes (Truncated is then set).
	Before, After json.RawMessage
	// The field-level changes, as Recorder computes them; nil unless both
	// states are there.
	Changes   []Change
	Truncated bool
}

// Derive computes the changes between before and after (JSON; either may be
// empty) and redacts both, with this package's defaults: DefaultSensitiveKeys,
// DefaultIdentityKeys, DefaultMaxChanges and DefaultMaxSnapshotBytes. The
// audit service applies it to events that arrive with states but no changes,
// so every producer's events read alike.
func Derive(before, after json.RawMessage) (Derived, error) {
	var out Derived
	b, err := normalize(raw(before))
	if err != nil {
		return out, err
	}
	a, err := normalize(raw(after))
	if err != nil {
		return out, err
	}
	red := newRedactor(nil)
	if b != nil && a != nil {
		d := differ{red: red, ignore: map[string]bool{}, idKeys: DefaultIdentityKeys, max: DefaultMaxChanges}
		d.diff("", false, b, a)
		out.Changes, out.Truncated = d.changes, d.truncated
	}
	for _, s := range []struct {
		v   any
		dst *json.RawMessage
	}{{b, &out.Before}, {a, &out.After}} {
		if s.v == nil {
			continue
		}
		if *s.dst, err = json.Marshal(red.value(s.v, false)); err != nil {
			return out, err
		}
	}
	if len(out.Before) > DefaultMaxSnapshotBytes || len(out.After) > DefaultMaxSnapshotBytes {
		out.Before, out.After, out.Truncated = nil, nil, true
	}
	return out, nil
}

// raw is nil for an absent or null state, so it isn't diffed as a value.
func raw(state json.RawMessage) any {
	if len(state) == 0 || string(state) == "null" {
		return nil
	}
	return state
}
