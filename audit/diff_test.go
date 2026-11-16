package audit

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func num(s string) json.Number { return json.Number(s) }

func TestDiffNestedObjects(t *testing.T) {
	before := map[string]any{
		"name":    "Lamp",
		"price":   10,
		"address": map[string]any{"city": "Tehran", "geo": map[string]any{"lat": 35.7, "lng": 51.4}},
		"legacy":  true,
	}
	after := map[string]any{
		"name":    "Lamp",
		"price":   12.0,
		"address": map[string]any{"city": "Shiraz", "geo": map[string]any{"lat": 35.7, "lng": 51.4}, "zip": "71"},
	}

	changes, err := Diff(before, after)
	require.NoError(t, err)
	assert.Equal(t, []Change{
		{Op: OpReplace, Path: "/address/city", From: "Tehran", To: "Shiraz"},
		{Op: OpAdd, Path: "/address/zip", To: "71"},
		{Op: OpRemove, Path: "/legacy", From: true},
		{Op: OpReplace, Path: "/price", From: num("10"), To: num("12")},
	}, changes)
}

func TestDiffNumbersCompareByValue(t *testing.T) {
	changes, err := Diff(
		json.RawMessage(`{"a": 10, "b": 1e3, "big": 12345678901234567890}`),
		json.RawMessage(`{"a": 10.0, "b": 1000, "big": 12345678901234567891}`),
	)
	require.NoError(t, err)
	assert.Equal(t, []Change{
		{Op: OpReplace, Path: "/big", From: num("12345678901234567890"), To: num("12345678901234567891")},
	}, changes, "10 == 10.0 and 1e3 == 1000; big integers keep their precision")
}

func TestDiffArraysByIndex(t *testing.T) {
	changes, err := Diff(
		map[string]any{"tags": []string{"home", "lamp", "old"}},
		map[string]any{"tags": []string{"home", "light"}},
	)
	require.NoError(t, err)
	assert.Equal(t, []Change{
		{Op: OpReplace, Path: "/tags/1", From: "lamp", To: "light"},
		{Op: OpRemove, Path: "/tags/2", From: "old"},
	}, changes)
}

func TestDiffArraysByIdentity(t *testing.T) {
	type variant struct {
		ID    string `json:"id"`
		Color string `json:"color"`
		Stock int    `json:"stock"`
	}
	before := map[string]any{"variants": []variant{{"v1", "red", 3}, {"v2", "blue", 1}}}
	// v0 inserted at the front: by index every element would look changed.
	after := map[string]any{"variants": []variant{{"v0", "green", 9}, {"v1", "red", 5}}}

	changes, err := Diff(before, after)
	require.NoError(t, err)
	assert.Equal(t, []Change{
		{Op: OpAdd, Path: "/variants/id=v0", To: map[string]any{"id": "v0", "color": "green", "stock": num("9")}},
		{Op: OpReplace, Path: "/variants/id=v1/stock", From: num("3"), To: num("5")},
		{Op: OpRemove, Path: "/variants/id=v2", From: map[string]any{"id": "v2", "color": "blue", "stock": num("1")}},
	}, changes)
}

func TestDiffIdentityNeedsUniqueIDsOnEveryElement(t *testing.T) {
	changes, err := Diff(
		json.RawMessage(`{"items":[{"id":1,"q":1},{"id":1,"q":2}]}`),
		json.RawMessage(`{"items":[{"id":1,"q":1},{"id":1,"q":3}]}`),
	)
	require.NoError(t, err)
	assert.Equal(t, []Change{{Op: OpReplace, Path: "/items/1/q", From: num("2"), To: num("3")}}, changes,
		"duplicate ids fall back to index")

	changes, err = Diff(
		json.RawMessage(`{"items":[{"uuid":"a","q":1}]}`),
		json.RawMessage(`{"items":[{"uuid":"a","q":2}]}`),
	)
	require.NoError(t, err)
	assert.Equal(t, "/items/uuid=a/q", changes[0].Path, "uuid identifies when id is absent")
}

func TestDiffTypeChangeAndNull(t *testing.T) {
	changes, err := Diff(
		json.RawMessage(`{"a": {"x": 1}, "b": null, "c": "s"}`),
		json.RawMessage(`{"a": "flat", "b": 2, "c": null}`),
	)
	require.NoError(t, err)
	assert.Equal(t, []Change{
		{Op: OpReplace, Path: "/a", From: map[string]any{"x": num("1")}, To: "flat"},
		{Op: OpReplace, Path: "/b", From: nil, To: num("2")},
		{Op: OpReplace, Path: "/c", From: "s", To: nil},
	}, changes)
}

func TestDiffEscapesPointerTokens(t *testing.T) {
	changes, err := Diff(map[string]any{"a/b": 1, "m~n": 1}, map[string]any{"a/b": 2, "m~n": 2})
	require.NoError(t, err)
	assert.Equal(t, "/a~1b", changes[0].Path)
	assert.Equal(t, "/m~0n", changes[1].Path)
}

func TestDiffUsesJSONTags(t *testing.T) {
	type user struct {
		Name    string `json:"full_name"`
		private string //nolint:unused // proves unexported fields are ignored
		Skip    string `json:"-"`
	}
	changes, err := Diff(user{Name: "a", Skip: "x"}, user{Name: "b", Skip: "y"})
	require.NoError(t, err)
	assert.Equal(t, []Change{{Op: OpReplace, Path: "/full_name", From: "a", To: "b"}}, changes)
}

func TestDiffMaxChanges(t *testing.T) {
	before, after := map[string]int{}, map[string]int{}
	for i := range 10 {
		k := string(rune('a' + i))
		before[k], after[k] = i, i+1
	}
	b, _ := normalize(before)
	a, _ := normalize(after)
	d := differ{red: newRedactor(nil), idKeys: DefaultIdentityKeys, max: 3}
	d.diff("", false, b, a)
	assert.Len(t, d.changes, 3)
	assert.True(t, d.truncated)
}

func TestChangeJSONKeepsNullSides(t *testing.T) {
	out, err := json.Marshal([]Change{
		{Op: OpReplace, Path: "/b", From: nil, To: 2},
		{Op: OpAdd, Path: "/c", To: nil},
		{Op: OpRemove, Path: "/d", From: 1},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"op":"replace","path":"/b","from":null,"to":2},
		{"op":"add","path":"/c","to":null},
		{"op":"remove","path":"/d","from":1}
	]`, string(out))
}
