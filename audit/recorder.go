package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/salahfarzin/utils/internal/safelog"
	"github.com/salahfarzin/utils/middlewares"
	"github.com/salahfarzin/utils/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Message is one encoded event, ready for a transport.
type Message struct {
	Key     []byte
	Value   []byte
	Headers map[string]string
}

// Publisher delivers encoded events. NewKafkaPublisher is the production one.
type Publisher interface {
	Publish(ctx context.Context, msg Message) error
	Close() error
}

// Defaults for New. Each can be changed with a RecorderOption.
const (
	DefaultQueueSize        = 1024
	DefaultMaxSnapshotBytes = 256 << 10
	defaultAttempts         = 3
	defaultRetryBackoff     = 200 * time.Millisecond
	defaultPublishTimeout   = 10 * time.Second
)

// Recorder builds audit events from the request context and publishes them.
// Build one per service at startup and share it; Close it on shutdown.
type Recorder struct {
	service string
	pub     Publisher
	cfg     config
	red     *redactor
	now     func() time.Time

	mu     sync.RWMutex
	closed bool
	queue  chan Message
	done   chan struct{}

	dropped atomic.Int64
	failed  atomic.Int64
}

type config struct {
	queueSize        int
	maxSnapshotBytes int
	maxChanges       int
	attempts         int
	retryBackoff     time.Duration
	publishTimeout   time.Duration
	ignore           []string
	redactKeys       []string
	identityKeys     []string
	log              *zap.Logger
}

// RecorderOption configures New.
type RecorderOption func(*config)

// WithQueueSize sets how many events can wait to be published before Record
// starts dropping them (default 1024).
func WithQueueSize(n int) RecorderOption { return func(c *config) { c.queueSize = n } }

// WithMaxSnapshotBytes sets the size above which Before/After are dropped
// (the changes are kept and Truncated is set). Default 256 KiB.
func WithMaxSnapshotBytes(n int) RecorderOption {
	return func(c *config) { c.maxSnapshotBytes = n }
}

// WithIgnoreFields leaves fields with these names (at any depth) out of the
// changes, e.g. "updated_at". The snapshots still contain them.
func WithIgnoreFields(names ...string) RecorderOption {
	return func(c *config) { c.ignore = append(c.ignore, names...) }
}

// WithRedactKeys adds field names to redact, on top of DefaultSensitiveKeys.
func WithRedactKeys(keys ...string) RecorderOption {
	return func(c *config) { c.redactKeys = append(c.redactKeys, keys...) }
}

// WithIdentityKeys replaces the keys that identify array elements (default
// "id", "uuid").
func WithIdentityKeys(keys ...string) RecorderOption {
	return func(c *config) { c.identityKeys = keys }
}

// WithLogger sets where the recorder reports dropped and unpublished events.
// By default it uses github.com/salahfarzin/logger once logger.Init has run,
// and zap's production logger (stderr) before that.
func WithLogger(l *zap.Logger) RecorderOption { return func(c *config) { c.log = l } }

// New returns a Recorder for service that publishes through pub, and starts
// its background publisher.
func New(service string, pub Publisher, opts ...RecorderOption) (*Recorder, error) {
	if service == "" {
		return nil, errors.New("audit: service name is required")
	}
	if pub == nil {
		return nil, errors.New("audit: publisher is required")
	}
	cfg := config{
		queueSize:        DefaultQueueSize,
		maxSnapshotBytes: DefaultMaxSnapshotBytes,
		maxChanges:       DefaultMaxChanges,
		attempts:         defaultAttempts,
		retryBackoff:     defaultRetryBackoff,
		publishTimeout:   defaultPublishTimeout,
		identityKeys:     DefaultIdentityKeys,
	}
	for _, o := range opts {
		o(&cfg)
	}
	r := &Recorder{
		service: service,
		pub:     pub,
		cfg:     cfg,
		red:     newRedactor(cfg.redactKeys),
		now:     time.Now,
		queue:   make(chan Message, max(cfg.queueSize, 1)),
		done:    make(chan struct{}),
	}
	go r.run()
	return r, nil
}

// Option sets one part of a recorded event.
type Option func(*recordOptions)

type recordOptions struct {
	before, after any
	outcome       Outcome
	reason        string
	meta          map[string]any
	actor         *Actor
	ignore        []string
}

// Before is the record's state before the action: any JSON-encodable value,
// at any depth. Omit it on create.
func Before(v any) Option { return func(o *recordOptions) { o.before = v } }

// After is the record's state after the action. Omit it on delete.
func After(v any) Option { return func(o *recordOptions) { o.after = v } }

// Failed marks the event a failure with err as the reason. A nil err leaves
// the outcome alone, so Failed(err) can be passed unconditionally.
func Failed(err error) Option {
	return func(o *recordOptions) {
		if err != nil {
			o.outcome, o.reason = Failure, err.Error()
		}
	}
}

// Result sets the outcome and reason explicitly.
func Result(outcome Outcome, reason string) Option {
	return func(o *recordOptions) { o.outcome, o.reason = outcome, reason }
}

// Meta adds a metadata entry. Keep it small; it's redacted like the snapshots.
func Meta(key string, value any) Option {
	return func(o *recordOptions) {
		if o.meta == nil {
			o.meta = map[string]any{}
		}
		o.meta[key] = value
	}
}

// As sets the actor explicitly, for work with no user in the context: a
// consumer acting for a user, or Actor{Type: ActorService, ID: "billing"}.
func As(a Actor) Option { return func(o *recordOptions) { o.actor = &a } }

// IgnoreFields leaves these field names out of this event's changes.
func IgnoreFields(names ...string) Option {
	return func(o *recordOptions) { o.ignore = append(o.ignore, names...) }
}

// Record builds an event and queues it for publishing. It never blocks and
// never fails the caller: if the queue is full or the event can't be built,
// it is dropped, counted (Dropped, Failed) and logged.
func (r *Recorder) Record(ctx context.Context, action string, res Resource, opts ...Option) {
	msg, err := r.build(ctx, action, res, opts)
	if err != nil {
		r.failed.Add(1)
		r.logger().Error("audit: event not built", zap.Error(err), zap.String("action", action))
		return
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		r.dropped.Add(1)
		r.logger().Error("audit: recorder closed, event dropped", zap.String("action", action))
		return
	}
	select {
	case r.queue <- msg:
	default:
		r.dropped.Add(1)
		r.logger().Error("audit: queue full, event dropped", zap.String("action", action))
	}
}

// RecordSync builds and publishes an event before returning, retrying within
// ctx. Use it where losing the event is unacceptable and the caller can fail
// the operation instead.
func (r *Recorder) RecordSync(ctx context.Context, action string, res Resource, opts ...Option) error {
	msg, err := r.build(ctx, action, res, opts)
	if err != nil {
		return err
	}
	return r.publish(ctx, msg)
}

// Dropped is the number of events dropped because the queue was full or the
// recorder was closed.
func (r *Recorder) Dropped() int64 { return r.dropped.Load() }

// Failed is the number of events that couldn't be built, or were still not
// published after retries.
func (r *Recorder) Failed() int64 { return r.failed.Load() }

// Close stops accepting events, publishes those still queued (until ctx is
// done) and closes the publisher.
func (r *Recorder) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	close(r.queue)
	r.mu.Unlock()

	select {
	case <-r.done:
	case <-ctx.Done():
		return errors.Join(ctx.Err(), r.pub.Close())
	}
	return r.pub.Close()
}

func (r *Recorder) run() {
	defer close(r.done)
	for msg := range r.queue {
		if err := r.publish(context.Background(), msg); err != nil {
			r.failed.Add(1)
			r.logger().Error("audit: event not published", zap.Error(err), zap.ByteString("key", msg.Key))
		}
	}
}

func (r *Recorder) publish(ctx context.Context, msg Message) error {
	var err error
	for attempt := range r.cfg.attempts {
		if attempt > 0 {
			select {
			case <-time.After(r.cfg.retryBackoff << (attempt - 1)):
			case <-ctx.Done():
				return errors.Join(err, ctx.Err())
			}
		}
		actx, cancel := context.WithTimeout(ctx, r.cfg.publishTimeout)
		err = r.pub.Publish(actx, msg)
		cancel()
		if err == nil {
			return nil
		}
	}
	return fmt.Errorf("audit: publish failed after %d attempts: %w", r.cfg.attempts, err)
}

// build turns a Record call into an encoded event.
func (r *Recorder) build(ctx context.Context, action string, res Resource, opts []Option) (Message, error) {
	if action == "" {
		return Message{}, errors.New("audit: action is required")
	}
	o := recordOptions{outcome: Success}
	for _, opt := range opts {
		opt(&o)
	}

	ev := Event{
		EventID:       uuid.NewV7().String(),
		SchemaVersion: SchemaVersion,
		OccurredAt:    r.now().UTC(),
		Service:       r.service,
		Action:        action,
		Outcome:       o.outcome,
		Reason:        o.reason,
		Resource:      res,
	}

	st := stateFrom(ctx)
	ev.Actor = r.actor(ctx, st, o.actor)
	if st != nil {
		_, _, ev.RequestID, _, _ = st.snapshot()
		st.markRecorded()
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		ev.TraceID, ev.SpanID = sc.TraceID().String(), sc.SpanID().String()
	} else if id, ok := ctx.Value(tracing.TraceIDKey).(string); ok {
		ev.TraceID = id
	}

	var problems []string
	before, err := normalize(o.before)
	if err != nil {
		problems = append(problems, "before: "+err.Error())
	}
	after, err := normalize(o.after)
	if err != nil {
		problems = append(problems, "after: "+err.Error())
	}

	if before != nil && after != nil {
		d := differ{
			red:    r.red,
			ignore: make(map[string]bool),
			idKeys: r.cfg.identityKeys,
			max:    r.cfg.maxChanges,
		}
		for _, f := range append(r.cfg.ignore, o.ignore...) {
			d.ignore[f] = true
		}
		d.diff("", false, before, after)
		ev.Changes, ev.Truncated = d.changes, d.truncated
	}

	if ev.Before, err = r.snapshot(before); err != nil {
		problems = append(problems, "before: "+err.Error())
	}
	if ev.After, err = r.snapshot(after); err != nil {
		problems = append(problems, "after: "+err.Error())
	}
	if len(ev.Before) > r.cfg.maxSnapshotBytes || len(ev.After) > r.cfg.maxSnapshotBytes {
		ev.Before, ev.After, ev.Truncated = nil, nil, true
	}

	if len(o.meta) > 0 {
		meta, err := normalize(o.meta)
		if err != nil {
			problems = append(problems, "metadata: "+err.Error())
		} else if m, ok := r.red.value(meta, false).(map[string]any); ok {
			ev.Metadata = m
		}
	}
	if len(problems) > 0 {
		if ev.Metadata == nil {
			ev.Metadata = map[string]any{}
		}
		ev.Metadata["audit_error"] = problems
	}

	value, err := json.Marshal(ev)
	if err != nil {
		return Message{}, fmt.Errorf("audit: encode event: %w", err)
	}

	headers := map[string]string{
		"content-type":         "application/json",
		"audit-schema-version": fmt.Sprint(SchemaVersion),
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(headers))

	key := ev.EventID
	if res.Type != "" || res.ID != "" {
		key = res.Type + "/" + res.ID
	}
	return Message{Key: []byte(key), Value: value, Headers: headers}, nil
}

func (r *Recorder) snapshot(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(r.red.value(v, false))
}

// actor resolves who acted: an explicit As, else the authenticated user, else
// anonymous inside a request or system outside one. IP, user agent and
// Referer come from the request when the actor doesn't set them.
func (r *Recorder) actor(ctx context.Context, st *requestState, explicit *Actor) Actor {
	var a Actor
	var ip, ua string
	var captured *middlewares.User
	if st != nil {
		ip, ua, _, captured, _ = st.snapshot()
	}

	switch {
	case explicit != nil:
		a = *explicit
	default:
		user, ok := middlewares.GetUser(ctx)
		if !ok || user == nil {
			user = captured
		}
		switch {
		case user != nil:
			a = Actor{Type: ActorUser, ID: user.Uuid, Email: user.Email}
			if a.ID == "" {
				a.ID = user.ID
			}
		case st != nil:
			a = Actor{Type: ActorAnonymous}
		default:
			a = Actor{Type: ActorSystem}
		}
	}
	if a.IP == "" {
		a.IP = ip
	}
	if a.UserAgent == "" {
		a.UserAgent = ua
	}
	if a.Referer == "" && st != nil {
		a.Referer = st.getReferer()
	}
	return a
}

func (r *Recorder) logger() *zap.Logger {
	if r.cfg.log != nil {
		return r.cfg.log
	}
	return safelog.Get()
}
