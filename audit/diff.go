package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"strings"
)

// DefaultMaxChanges caps the changes recorded per event; past it the list is
// cut and Event.Truncated is set.
const DefaultMaxChanges = 500

// DefaultIdentityKeys are the keys that identify array elements: arrays of
// objects that all carry a unique one of these are diffed by identity, not
// by position.
var DefaultIdentityKeys = []string{"id", "uuid"}

// normalize turns v into its generic JSON form (maps, slices, json.Number,
// string, bool, nil) through its JSON encoding, so struct tags apply and any
// two values compare the same way. json.RawMessage is used as is.
func normalize(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	data, ok := v.(json.RawMessage)
	if !ok {
		var err error
		if data, err = json.Marshal(v); err != nil {
			return nil, err
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// differ computes the field-level changes between two normalized values,
// redacting values under sensitive keys as it emits them.
type differ struct {
	red       *redactor
	ignore    map[string]bool
	idKeys    []string
	max       int
	changes   []Change
	truncated bool
}

// Diff returns the field-level changes from before to after, using the
// default identity keys, the default redaction and no ignored fields. Any
// values JSON can encode work; struct json tags apply.
func Diff(before, after any) ([]Change, error) {
	b, err := normalize(before)
	if err != nil {
		return nil, fmt.Errorf("audit: before: %w", err)
	}
	a, err := normalize(after)
	if err != nil {
		return nil, fmt.Errorf("audit: after: %w", err)
	}
	d := differ{red: newRedactor(nil), idKeys: DefaultIdentityKeys, max: DefaultMaxChanges}
	d.diff("", false, b, a)
	return d.changes, nil
}

func (d *differ) diff(path string, sensitive bool, a, b any) {
	if d.truncated {
		return
	}
	switch av := a.(type) {
	case map[string]any:
		if bv, ok := b.(map[string]any); ok {
			d.objects(path, sensitive, av, bv)
			return
		}
	case []any:
		if bv, ok := b.([]any); ok {
			d.arrays(path, sensitive, av, bv)
			return
		}
	}
	if !equal(a, b) {
		d.emit(OpReplace, path, sensitive, a, b)
	}
}

func (d *differ) objects(path string, sensitive bool, a, b map[string]any) {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)

	for _, k := range keys {
		if d.ignore[k] {
			continue
		}
		p := path + "/" + escapePointer(k)
		s := sensitive || d.red.sensitiveKey(k)
		av, inA := a[k]
		bv, inB := b[k]
		switch {
		case inA && inB:
			d.diff(p, s, av, bv)
		case inB:
			d.emit(OpAdd, p, s, nil, bv)
		default:
			d.emit(OpRemove, p, s, av, nil)
		}
	}
}

func (d *differ) arrays(path string, sensitive bool, a, b []any) {
	if key, ok := identityKey(d.idKeys, a, b); ok {
		d.arraysByIdentity(path, sensitive, key, a, b)
		return
	}

	n := min(len(a), len(b))
	for i := range n {
		d.diff(fmt.Sprintf("%s/%d", path, i), sensitive, a[i], b[i])
	}
	for i := n; i < len(b); i++ {
		d.emit(OpAdd, fmt.Sprintf("%s/%d", path, i), sensitive, nil, b[i])
	}
	for i := n; i < len(a); i++ {
		d.emit(OpRemove, fmt.Sprintf("%s/%d", path, i), sensitive, a[i], nil)
	}
}

// arraysByIdentity pairs elements by their key: kept ones are diffed, new
// ones added, missing ones removed. Reordering alone is not a change.
func (d *differ) arraysByIdentity(path string, sensitive bool, key string, a, b []any) {
	elemPath := func(id string) string { return path + "/" + escapePointer(key+"="+id) }
	before := make(map[string]any, len(a))
	for _, e := range a {
		before[identity(e, key)] = e
	}
	after := make(map[string]bool, len(b))
	for _, e := range b {
		id := identity(e, key)
		after[id] = true
		if old, ok := before[id]; ok {
			d.diff(elemPath(id), sensitive, old, e)
		} else {
			d.emit(OpAdd, elemPath(id), sensitive, nil, e)
		}
	}
	for _, e := range a {
		if id := identity(e, key); !after[id] {
			d.emit(OpRemove, elemPath(id), sensitive, e, nil)
		}
	}
}

func (d *differ) emit(op Op, path string, sensitive bool, from, to any) {
	if len(d.changes) >= d.max {
		d.truncated = true
		return
	}
	c := Change{Op: op, Path: path}
	if op != OpAdd {
		c.From = d.red.value(from, sensitive)
	}
	if op != OpRemove {
		c.To = d.red.value(to, sensitive)
	}
	d.changes = append(d.changes, c)
}

// identityKey returns the first of keys that every element of a and b (all
// objects) carries as a scalar, unique within each array.
func identityKey(keys []string, a, b []any) (string, bool) {
	if len(a)+len(b) == 0 {
		return "", false
	}
	for _, key := range keys {
		if uniqueIdentities(a, key) && uniqueIdentities(b, key) {
			return key, true
		}
	}
	return "", false
}

func uniqueIdentities(arr []any, key string) bool {
	seen := make(map[string]bool, len(arr))
	for _, e := range arr {
		obj, ok := e.(map[string]any)
		if !ok {
			return false
		}
		var id string
		switch v := obj[key].(type) {
		case string:
			id = v
		case json.Number:
			id = v.String()
		default:
			return false
		}
		if id == "" || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func identity(e any, key string) string {
	switch v := e.(map[string]any)[key].(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	}
	return ""
}

func equal(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case json.Number:
		y, ok := b.(json.Number)
		return ok && numbersEqual(x, y)
	}
	return reflect.DeepEqual(a, b)
}

// numbersEqual compares by value, exactly: 10 == 10.0 == 1e1, and large
// integers don't lose precision.
func numbersEqual(x, y json.Number) bool {
	if x == y {
		return true
	}
	rx, okx := new(big.Rat).SetString(string(x))
	ry, oky := new(big.Rat).SetString(string(y))
	return okx && oky && rx.Cmp(ry) == 0
}

var pointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

func escapePointer(s string) string { return pointerEscaper.Replace(s) }

// MarshalJSON writes `from` for every op but add and `to` for every op but
// remove, even when null, so a replace from null isn't mistaken for an add.
// Fields are written in a fixed order: op, path, from, to.
func (c Change) MarshalJSON() ([]byte, error) {
	w := struct {
		Op   Op              `json:"op"`
		Path string          `json:"path"`
		From json.RawMessage `json:"from,omitempty"`
		To   json.RawMessage `json:"to,omitempty"`
	}{Op: c.Op, Path: c.Path}
	var err error
	if c.Op != OpAdd {
		if w.From, err = json.Marshal(c.From); err != nil {
			return nil, err
		}
	}
	if c.Op != OpRemove {
		if w.To, err = json.Marshal(c.To); err != nil {
			return nil, err
		}
	}
	return json.Marshal(w)
}
