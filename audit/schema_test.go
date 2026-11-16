package audit

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/salahfarzin/utils/middlewares"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The JSON Schema is what Python producers follow; it must describe exactly
// what this package sends.
func TestSchemaMatchesEvent(t *testing.T) {
	raw, err := os.ReadFile("schema/event.v1.json")
	require.NoError(t, err)
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))

	pub := &memPublisher{}
	rec := newTestRecorder(t, pub)
	ctx := context.WithValue(context.Background(), middlewares.UserKey, &middlewares.User{Uuid: "u", Email: "e"})
	ctx = withState(ctx, &requestState{ip: "1.2.3.4", userAgent: "ua", requestID: "r"})
	rec.Record(ctx, "a.update", Resource{Type: "a", ID: "1"},
		Before(map[string]int{"x": 1}), After(map[string]int{"x": 2}), Meta("k", "v"), Failed(assert.AnError))
	closeRecorder(t, rec)

	var sent map[string]any
	require.NoError(t, json.Unmarshal(pub.msgs[0].Value, &sent))

	for _, k := range schema.Required {
		assert.Contains(t, sent, k, "required by the schema")
	}
	for k := range sent {
		assert.Contains(t, schema.Properties, k, "sent but not in the schema")
	}
	fields := []string{}
	for k := range schema.Properties {
		fields = append(fields, k)
	}
	slices.Sort(fields)
	// Every schema property is reachable: the event above sets all but these.
	for _, k := range fields {
		if k != "truncated" && k != "trace_id" && k != "span_id" {
			assert.Contains(t, sent, k, "schema property never produced")
		}
	}
}
