# audit

Records who did what to which record, whether it worked, and what changed (the full
before/after state, nested objects included), and publishes it to Kafka topic
`audit-events`. The `audit` service stores it, and `~/projects/dashboard` shows it.
Design: `audit/docs/superpowers/specs/2026-05-30-audit-log-design.md`.

This is not the application log. Use `logger` for debugging; use `audit` for actions a
person might later ask about ("who changed this price?", "who tried to delete that
user?").

## Wiring a service

```go
pub, err := audit.NewKafkaPublisher(audit.KafkaConfig{
    Brokers:  strings.Split(os.Getenv("KAFKA_BROKERS"), ","),
    Username: os.Getenv("KAFKA_USERNAME"), // optional: SASL/SCRAM-SHA-512
    Password: os.Getenv("KAFKA_PASSWORD"),
    UseSSL:   os.Getenv("KAFKA_USE_SSL") == "true",
})
if err != nil { log.Fatal(err) }
rec, err := audit.New("product", pub, audit.WithIgnoreFields("updated_at"))
if err != nil { log.Fatal(err) }
defer rec.Close(context.Background()) // publishes what's still queued

authFn := middlewares.CachedAuth(middlewares.NewAuthMeFunc(authURL, nil), time.Minute)

grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
    audit.UnaryServerInterceptor(rec),                 // before auth…
    middlewares.AuthUnaryInterceptor(authFn,
        middlewares.OnAuthenticated(audit.CaptureUser), // …which tells it who called
        middlewares.RequirePermissions(perms)),
))
```

The interceptor alone gives you, with no handler changes:

- every **denied** call: `PermissionDenied` always, `Unauthenticated` on write methods,
  naming the caller when the token was valid;
- every **write** method (`Create*`, `Update*`, `Delete*`, `Set*`, `Add*`, `Remove*`,
  `Cancel*`, … or exactly the ones in `audit.Methods(...)`) as success or failure;
- the caller's IP (`x-forwarded-for` from the gateway, else the peer), user agent,
  `Referer` and `x-request-id`, for these events and for `Record` calls in handlers. The
  Referer keeps only scheme, host and path: its query and fragment can carry one-time
  tokens (password reset, magic links, OAuth `code`), which must not reach the audit log.
  Across sites browsers usually send only the origin.

`audit.Skip("/pkg.Service/Method")` turns a method off. Streaming calls aren't
intercepted; record them with `Record`.

**grpc-gateway services:** use only the gRPC interceptor. REST calls reach it through
the gateway.

**Plain REST services:**

```go
handler := audit.HTTPMiddleware(rec,
    audit.WrapHTTPAuth(middlewares.AuthMiddleware(authFn)), // auth runs inside audit
    audit.Skip("/health", "/metrics"),                       // path prefixes
)(mux)
```

It records 403s always, 401s on writes, and every `POST/PUT/PATCH/DELETE` (failure
when the status is 400 or higher).

## Recording with before/after

The automatic events say *that* a write happened. To say *what* changed, record it in
the handler with the record's state before and after:

```go
old, err := s.repo.Get(ctx, id)
...
updated, err := s.repo.Update(ctx, id, input)
rec.Record(ctx, "product.update", audit.Resource{Type: "product", ID: id},
    audit.Before(old), audit.After(updated), audit.Failed(err))
```

- When a handler calls `Record`, the interceptor's automatic event for that call is
  skipped. You get one event, the detailed one.
- `Before`/`After` take any JSON-encodable value: structs (their `json` tags apply),
  maps, `json.RawMessage`. Omit `Before` on create and `After` on delete.
- `Failed(err)` sets outcome `failure` with the error as the reason. A nil err changes
  nothing, so it can always be passed.
- Other options: `Result(audit.Denied, "not the owner")`, `Meta("key", v)` for small
  context, `As(audit.Actor{Type: audit.ActorService, ID: "billing"})` when there's no
  user in the context, `IgnoreFields("version")`.
- The actor is the user from `middlewares.GetUser(ctx)`. Without one it's `anonymous`
  inside a request, or `system` outside one (jobs, consumers).
- `trace_id`/`span_id` come from the active OpenTelemetry span (else `utils/tracing`'s
  id), and the `traceparent` header goes with the message.

### What the changes look like

```go
before := Product{Price: 10, Tags: []string{"home"},
    Variants: []Variant{{ID: "v1", Stock: 3}}}
after  := Product{Price: 12, Tags: []string{"home", "sale"},
    Variants: []Variant{{ID: "v0", Stock: 9}, {ID: "v1", Stock: 5}}}
```
```json
[
  {"op":"replace","path":"/price","from":10,"to":12},
  {"op":"add","path":"/tags/1","to":"sale"},
  {"op":"add","path":"/variants/id=v0","to":{"id":"v0","stock":9}},
  {"op":"replace","path":"/variants/id=v1/stock","from":3,"to":5}
]
```

- Nested objects are compared at any depth.
- Arrays of objects that each have a unique `id` (or `uuid`; change it with
  `WithIdentityKeys`) are matched by that id, so inserting `v0` at the front doesn't
  mark `v1` as changed. Reordering alone isn't a change. Other arrays compare by index.
- Numbers compare by value (`10` equals `10.0`).
- `audit.Diff(before, after)` gives the same list without recording anything.

### Secrets

Fields whose name contains one of these words (any case or separator) are replaced by
`"[REDACTED]"` in `before`, `after`, `changes` and `metadata`, at any depth:
`password`, `passwd`, `pwd`, `secret`, `token`, `credential(s)`, `api_key`/`apikey`,
`authorization`, `cookie`, `private_key`, `card_number`, `cvv`, `cvc`, `iban`, `ssn`,
`otp`. Add your own with `WithRedactKeys("salary")`.

Matching is on whole words: `access_token`, `refreshToken` and `APIKey` are redacted,
while `tokens_used` and `max_tokens` are not. A changed password still appears, as
`{"op":"replace","path":"/password","from":"[REDACTED]","to":"[REDACTED]"}`.

### Size

If `before` or `after` encodes to more than 256 KiB (`WithMaxSnapshotBytes`), both are
left out and `truncated` is set. The changes are kept, up to 500. For big documents,
pass a projection of the record.

## Delivery

- **`Record` is asynchronous.** It queues the event (1024 by default,
  `WithQueueSize`) and returns. A background worker publishes with 3 attempts. A full
  queue or a failed publish never slows or fails the request: the event is counted
  (`rec.Dropped()`, `rec.Failed()`) and logged.
- **`RecordSync`** publishes before returning and returns the error. Use it where the
  operation should fail rather than go unaudited.
- **`Close`** stops accepting events, publishes the queue (until its context ends) and
  closes the publisher. Call it on shutdown.
- **Kafka:** key `<resource.type>/<resource.id>` (one record's events stay in order),
  all in-sync replicas must acknowledge, and the headers are `content-type`,
  `audit-schema-version` and `traceparent`.
- **Logging:** problems go to `logger` once `logger.Init` has run, to stderr before
  that, or to `WithLogger(l)`.

## Event format

`audit/schema/event.v1.json` (JSON Schema) is the contract, also for Python producers.
A test keeps it in sync with `audit.Event`.

### Producers in other languages

A service that can't use this package (Laravel, Python) publishes the same envelope to the
same topic, with the record's `before` and/or `after` states and **no `changes`**. The audit
service then derives them with `audit.Derive`, exactly as a `Recorder` would: the changes
(nested, list items matched by id), secrets redacted in the states and changes, and
oversized states dropped (`truncated`). So the producer needs no diff or redaction of its
own, and every service's events read alike. What it must set:

- `event_id`: a UUID (v7 preferred), new per event, so a re-delivery is stored once.
- `schema_version` 1, `occurred_at` (RFC 3339), `service`, `action`, `outcome`.
- `actor`: `{type: user, id, email, ip, user_agent, referer}` inside a request, `system` outside.
- `resource`: `{type, id}`. Use it as the Kafka key (`type/id`), so one record's events stay in order.
