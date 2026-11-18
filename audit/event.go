// Package audit records who did what to which record, and what changed, as
// schema-v1 events published to Kafka (topic "audit-events") for the audit
// service. See docs/audit.md for the event format and how to wire it.
package audit

import (
	"encoding/json"
	"time"
)

// SchemaVersion is the version of the event envelope this package produces.
const SchemaVersion = 1

// DefaultTopic is the Kafka topic audit events are published to.
const DefaultTopic = "audit-events"

// Outcome is how the audited operation ended.
type Outcome string

const (
	// Success: the operation completed.
	Success Outcome = "success"
	// Failure: the operation was attempted and errored.
	Failure Outcome = "failure"
	// Denied: the caller was not authenticated or not allowed.
	Denied Outcome = "denied"
)

// ActorType says what kind of caller performed the action.
type ActorType string

const (
	// ActorUser is an authenticated user (from middlewares.GetUser).
	ActorUser ActorType = "user"
	// ActorService is another service calling with its own identity.
	ActorService ActorType = "service"
	// ActorSystem is work with no caller: jobs, consumers, startup tasks.
	ActorSystem ActorType = "system"
	// ActorAnonymous is a request without a known identity (e.g. a failed login).
	ActorAnonymous ActorType = "anonymous"
)

// Actor is who performed the action and where they called from.
type Actor struct {
	Type      ActorType `json:"type"`
	ID        string    `json:"id,omitempty"`
	Email     string    `json:"email,omitempty"`
	IP        string    `json:"ip,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
	// Referer is the page the request came from: scheme, host and path only
	// (see cleanReferer). Browsers often send just the origin across sites.
	Referer string `json:"referer,omitempty"`
}

// Resource is the record the action was performed on.
type Resource struct {
	Type string `json:"type,omitempty"`
	ID   string `json:"id,omitempty"`
}

// Op is the kind of a field change.
type Op string

const (
	OpAdd     Op = "add"
	OpRemove  Op = "remove"
	OpReplace Op = "replace"
)

// Change is one field-level difference between Before and After.
//
// Path is JSON Pointer style ("/address/city", "/tags/1"). Elements of arrays
// matched by identity are written "<key>=<value>" ("/variants/id=v1/stock").
type Change struct {
	Op   Op     `json:"op"`
	Path string `json:"path"`
	From any    `json:"from,omitempty"`
	To   any    `json:"to,omitempty"`
}

// Event is the schema-v1 audit envelope, as published to Kafka.
type Event struct {
	EventID       string          `json:"event_id"`
	SchemaVersion int             `json:"schema_version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Service       string          `json:"service"`
	Action        string          `json:"action"`
	Outcome       Outcome         `json:"outcome"`
	Reason        string          `json:"reason,omitempty"`
	Actor         Actor           `json:"actor"`
	Resource      Resource        `json:"resource"`
	Before        json.RawMessage `json:"before,omitempty"`
	After         json.RawMessage `json:"after,omitempty"`
	Changes       []Change        `json:"changes,omitempty"`
	Truncated     bool            `json:"truncated,omitempty"`
	Metadata      map[string]any  `json:"metadata,omitempty"`
	RequestID     string          `json:"request_id,omitempty"`
	TraceID       string          `json:"trace_id,omitempty"`
	SpanID        string          `json:"span_id,omitempty"`
}
