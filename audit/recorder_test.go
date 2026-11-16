package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/salahfarzin/utils/middlewares"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// memPublisher keeps what it's given; fail makes the next n publishes fail;
// block, when set, holds every publish until closed.
type memPublisher struct {
	mu     sync.Mutex
	msgs   []Message
	fail   int
	block  chan struct{}
	closed bool
}

func (p *memPublisher) Publish(ctx context.Context, msg Message) error {
	if p.block != nil {
		select {
		case <-p.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail > 0 {
		p.fail--
		return errors.New("broker down")
	}
	p.msgs = append(p.msgs, msg)
	return nil
}

func (p *memPublisher) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	return nil
}

func (p *memPublisher) events(t *testing.T) []Event {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Event, len(p.msgs))
	for i, m := range p.msgs {
		require.NoError(t, json.Unmarshal(m.Value, &out[i]))
	}
	return out
}

func newTestRecorder(t *testing.T, pub *memPublisher, opts ...RecorderOption) *Recorder {
	t.Helper()
	rec, err := New("product", pub, append([]RecorderOption{WithLogger(zap.NewNop())}, opts...)...)
	require.NoError(t, err)
	rec.cfg.retryBackoff = time.Millisecond
	return rec
}

func closeRecorder(t *testing.T, rec *Recorder) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, rec.Close(ctx))
}

func TestNewValidates(t *testing.T) {
	_, err := New("", &memPublisher{})
	assert.Error(t, err)
	_, err = New("svc", nil)
	assert.Error(t, err)
}

func TestRecordUpdate(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	pub := &memPublisher{}
	rec := newTestRecorder(t, pub, WithIgnoreFields("updated_at"))

	type product struct {
		Name      string         `json:"name"`
		Price     int            `json:"price"`
		Secret    string         `json:"supplier_secret"`
		Attrs     map[string]any `json:"attrs"`
		UpdatedAt string         `json:"updated_at"`
	}
	old := product{Name: "Lamp", Price: 10, Secret: "s1", Attrs: map[string]any{"color": "red"}, UpdatedAt: "1"}
	updated := product{Name: "Lamp", Price: 12, Secret: "s2", Attrs: map[string]any{"color": "blue"}, UpdatedAt: "2"}

	user := &middlewares.User{ID: "7", Uuid: "u-1", Email: "jane@example.com"}
	ctx := context.WithValue(context.Background(), middlewares.UserKey, user)
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36},
		SpanID:     trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7},
		TraceFlags: trace.FlagsSampled,
	})
	ctx = trace.ContextWithSpanContext(ctx, sc)

	rec.Record(ctx, "product.update", Resource{Type: "product", ID: "42"},
		Before(old), After(updated), Meta("source", "admin"), Meta("api_token", "t"))
	closeRecorder(t, rec)

	require.Len(t, pub.msgs, 1)
	msg := pub.msgs[0]
	assert.Equal(t, "product/42", string(msg.Key))
	assert.Equal(t, "application/json", msg.Headers["content-type"])
	assert.Equal(t, "1", msg.Headers["audit-schema-version"])
	assert.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", msg.Headers["traceparent"])

	ev := pub.events(t)[0]
	id, err := uuid.Parse(ev.EventID)
	require.NoError(t, err)
	assert.EqualValues(t, 7, id[6]>>4, "event ids are UUIDv7")
	assert.Equal(t, SchemaVersion, ev.SchemaVersion)
	assert.Equal(t, "product", ev.Service)
	assert.Equal(t, Success, ev.Outcome)
	assert.Equal(t, Actor{Type: ActorUser, ID: "u-1", Email: "jane@example.com"}, ev.Actor)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", ev.TraceID)
	assert.Equal(t, "00f067aa0ba902b7", ev.SpanID)
	assert.Equal(t, map[string]any{"source": "admin", "api_token": Redacted}, ev.Metadata)

	assert.JSONEq(t, `{"name":"Lamp","price":10,"supplier_secret":"[REDACTED]","attrs":{"color":"red"},"updated_at":"1"}`, string(ev.Before))
	assert.JSONEq(t, `{"name":"Lamp","price":12,"supplier_secret":"[REDACTED]","attrs":{"color":"blue"},"updated_at":"2"}`, string(ev.After))
	raw, _ := json.Marshal(ev.Changes)
	assert.JSONEq(t, `[
		{"op":"replace","path":"/attrs/color","from":"red","to":"blue"},
		{"op":"replace","path":"/price","from":10,"to":12},
		{"op":"replace","path":"/supplier_secret","from":"[REDACTED]","to":"[REDACTED]"}
	]`, string(raw), "updated_at is ignored")
	assert.NotContains(t, string(msg.Value), "s1")
	assert.NotContains(t, string(msg.Value), "s2")
}

func TestRecordCreateAndDeleteHaveNoChanges(t *testing.T) {
	pub := &memPublisher{}
	rec := newTestRecorder(t, pub)
	rec.Record(context.Background(), "product.create", Resource{Type: "product", ID: "1"}, After(map[string]int{"a": 1}))
	rec.Record(context.Background(), "product.delete", Resource{Type: "product", ID: "1"}, Before(map[string]int{"a": 1}))
	closeRecorder(t, rec)

	evs := pub.events(t)
	require.Len(t, evs, 2)
	assert.Nil(t, evs[0].Before)
	assert.JSONEq(t, `{"a":1}`, string(evs[0].After))
	assert.Empty(t, evs[0].Changes)
	assert.JSONEq(t, `{"a":1}`, string(evs[1].Before))
	assert.Nil(t, evs[1].After)
}

func TestRecordActor(t *testing.T) {
	pub := &memPublisher{}
	rec := newTestRecorder(t, pub)

	rec.Record(context.Background(), "job.run", Resource{})
	rec.Record(withState(context.Background(), &requestState{ip: "203.0.113.7", userAgent: "curl"}), "user.login", Resource{})
	rec.Record(context.Background(), "invoice.send", Resource{}, As(Actor{Type: ActorService, ID: "billing"}))
	closeRecorder(t, rec)

	evs := pub.events(t)
	assert.Equal(t, Actor{Type: ActorSystem}, evs[0].Actor, "no request, no user: system")
	assert.Equal(t, Actor{Type: ActorAnonymous, IP: "203.0.113.7", UserAgent: "curl"}, evs[1].Actor, "a request without a user: anonymous")
	assert.Equal(t, Actor{Type: ActorService, ID: "billing"}, evs[2].Actor)
}

func TestRecordOutcome(t *testing.T) {
	pub := &memPublisher{}
	rec := newTestRecorder(t, pub)
	rec.Record(context.Background(), "a", Resource{}, Failed(nil))
	rec.Record(context.Background(), "b", Resource{}, Failed(errors.New("stock below zero")))
	rec.Record(context.Background(), "c", Resource{}, Result(Denied, "not owner"))
	closeRecorder(t, rec)

	evs := pub.events(t)
	assert.Equal(t, Success, evs[0].Outcome)
	assert.Equal(t, Failure, evs[1].Outcome)
	assert.Equal(t, "stock below zero", evs[1].Reason)
	assert.Equal(t, Denied, evs[2].Outcome)
}

func TestRecordLargeSnapshotsAreDroppedButChangesKept(t *testing.T) {
	pub := &memPublisher{}
	rec := newTestRecorder(t, pub, WithMaxSnapshotBytes(64))
	big := strings.Repeat("x", 100)
	rec.Record(context.Background(), "doc.update", Resource{Type: "doc", ID: "1"},
		Before(map[string]string{"body": big, "title": "a"}), After(map[string]string{"body": big, "title": "b"}))
	closeRecorder(t, rec)

	ev := pub.events(t)[0]
	assert.True(t, ev.Truncated)
	assert.Nil(t, ev.Before)
	assert.Nil(t, ev.After)
	assert.Equal(t, []Change{{Op: OpReplace, Path: "/title", From: "a", To: "b"}}, ev.Changes)
}

func TestRecordUnencodableStateStillRecords(t *testing.T) {
	pub := &memPublisher{}
	rec := newTestRecorder(t, pub)
	rec.Record(context.Background(), "x.update", Resource{}, Before(make(chan int)), After(map[string]int{"a": 1}))
	closeRecorder(t, rec)

	ev := pub.events(t)[0]
	assert.Contains(t, ev.Metadata["audit_error"], "before: json: unsupported type: chan int")
	assert.JSONEq(t, `{"a":1}`, string(ev.After))
}

func TestRecordRetriesThenSucceeds(t *testing.T) {
	pub := &memPublisher{fail: 2}
	rec := newTestRecorder(t, pub)
	rec.Record(context.Background(), "a", Resource{})
	closeRecorder(t, rec)
	assert.Len(t, pub.msgs, 1)
	assert.Zero(t, rec.Failed())
}

func TestRecordSyncReportsFailure(t *testing.T) {
	pub := &memPublisher{fail: 10}
	rec := newTestRecorder(t, pub)
	err := rec.RecordSync(context.Background(), "payment.capture", Resource{Type: "payment", ID: "9"})
	assert.ErrorContains(t, err, "broker down")
	assert.ErrorContains(t, err, "3 attempts")
	closeRecorder(t, rec)

	pub.fail = 0
	rec2 := newTestRecorder(t, pub)
	require.NoError(t, rec2.RecordSync(context.Background(), "payment.capture", Resource{Type: "payment", ID: "9"}))
	assert.Len(t, pub.msgs, 1, "published before RecordSync returned")
	closeRecorder(t, rec2)
}

func TestRecordDropsWhenQueueFull(t *testing.T) {
	pub := &memPublisher{block: make(chan struct{})}
	rec := newTestRecorder(t, pub, WithQueueSize(1))
	rec.Record(context.Background(), "a", Resource{})
	// Wait until the worker holds the first (blocked in Publish)…
	require.Eventually(t, func() bool { return len(rec.queue) == 0 }, time.Second, time.Millisecond)
	for range 4 {
		rec.Record(context.Background(), "a", Resource{})
	}
	// …then the queue takes one more and the other three are dropped.
	assert.Eventually(t, func() bool { return rec.Dropped() == 3 }, time.Second, time.Millisecond)
	close(pub.block)
	closeRecorder(t, rec)
	assert.Len(t, pub.msgs, 2)
}

func TestRecordAfterCloseIsDropped(t *testing.T) {
	pub := &memPublisher{}
	rec := newTestRecorder(t, pub)
	closeRecorder(t, rec)
	rec.Record(context.Background(), "a", Resource{})
	assert.EqualValues(t, 1, rec.Dropped())
	assert.True(t, pub.closed)
	assert.NoError(t, rec.Close(context.Background()), "Close is idempotent")
}

func TestRecordKeyFallsBackToEventID(t *testing.T) {
	pub := &memPublisher{}
	rec := newTestRecorder(t, pub)
	rec.Record(context.Background(), "a", Resource{})
	closeRecorder(t, rec)
	assert.Equal(t, pub.events(t)[0].EventID, string(pub.msgs[0].Key))
}
