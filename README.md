
# utils

Reusable utilities for Go projects supporting both REST (HTTP) and gRPC APIs.

## Features

- **Trace ID and User ID extraction/injection**
	- From gRPC metadata
	- From HTTP headers
- **Context helpers** for propagating trace/user IDs
- **Standardized error response** formatting for REST APIs

## Usage

### Trace/User ID Extraction

- **gRPC:**
	- `GetOrGenerateTraceID(ctx context.Context) string`
	- `GetUserIDFromContext(ctx context.Context) string`
- **REST:**
	- `GetOrGenerateTraceIDFromHeader(r *http.Request) string`
	- `GetUserIDFromHeader(r *http.Request) string`

### Injecting IDs into Context

- `InjectTraceIDToContext(ctx, traceID)`
- `InjectUserIDToContext(ctx, userID)`

### Extracting IDs from Context (Generic)

- `GetTraceIDFromContext(ctx)`
- `GetUserIDFromContextGeneric(ctx)`

### HTTP Header Utilities

- `SetTraceIDHeader(w, traceID)`
- `SetUserIDHeader(w, userID)`

### Error Response (REST)

- `WriteJSONError(w, status, errMsg, traceID)`

## Example

```go
// For REST handler
func handler(w http.ResponseWriter, r *http.Request) {
		traceID := utils.GetOrGenerateTraceIDFromHeader(r)
		userID := utils.GetUserIDFromHeader(r)
		ctx := utils.InjectTraceIDToContext(r.Context(), traceID)
		ctx = utils.InjectUserIDToContext(ctx, userID)
		// ...
		if err != nil {
				utils.WriteJSONError(w, http.StatusBadRequest, err.Error(), traceID)
				return
		}
}

// For gRPC interceptor
func interceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		traceID := utils.GetOrGenerateTraceID(ctx)
		userID := utils.GetUserIDFromContext(ctx)
		ctx = utils.InjectTraceIDToContext(ctx, traceID)
		ctx = utils.InjectUserIDToContext(ctx, userID)
		return handler(ctx, req)
}
```

## Authentication and permissions (`middlewares`)

Services behind auth resolve a caller's access token with auth's `/auth/me`.

```go
import mw "github.com/salahfarzin/utils/middlewares"

// One client for /auth/me; the user it returns carries auth's permissions.
authenticate := mw.NewAuthMeFunc("http://auth:8082/api/v1", nil)
```

### gRPC: `AuthUnaryInterceptor` / `AuthStreamInterceptor`

Use these on every gRPC server whose port is reachable, which includes any
port published in docker-compose. They check the token themselves, so a call
made straight to the gRPC port is checked like one through the REST gateway.

```go
server := grpc.NewServer(
	grpc.ChainUnaryInterceptor(
		mw.AuthUnaryInterceptor(authenticate,
			mw.PublicMethods("/grpc.health.v1.Health/"),
			mw.RequirePermissions(map[string]mw.Permission{
				"/orders.OrderService/List":   {Resource: "orders", Action: "view"},
				"/orders.OrderService/Cancel": {Resource: "orders", Action: "edit"},
			}),
		),
	),
	grpc.ChainStreamInterceptor(mw.AuthStreamInterceptor(authenticate)),
)
```

- **Token:** read from `authorization: Bearer <token>` metadata, which the REST
  gateway forwards, or from the `access_token` cookie.
- **The caller:** handlers read it with `mw.GetUser(ctx)` or
  `mw.GetUserFromContext(ctx)`.
- **Forged headers:** incoming `x-user-*` metadata is always dropped, even on
  public methods, so on the gRPC port it can't name the caller.
- **`PublicMethods`:** takes full methods (`/pkg.Service/Method`) or whole
  services (`/pkg.Service/`). These methods stay open without a token, but still
  see the caller when a valid token is sent.
- **`RequirePermissions`:** refuses with `PermissionDenied` unless the caller
  holds the listed permission. Methods not listed need only a valid token.

### Permissions in handlers

`/auth/me` returns `permissions`, `{resource: [actions]}`: the union of the
grants of the user's roles. Check permissions rather than role names, since
what a role grants is configured in auth.

```go
if err := mw.RequirePermission(ctx, "orders", "delete"); err != nil {
	return nil, err // Unauthenticated or PermissionDenied
}
// or, for a decision rather than a refusal:
user, _ := mw.GetUser(ctx)
if user.Can("orders", "edit") { ... }
```

### HTTP: `AuthMiddleware`

`AuthMiddleware(authenticate)` checks REST requests and forwards the user to
the gRPC handlers behind the gateway as `x-user-id`, `x-user-uuid` and
`x-user-roles`. Those headers are only as trustworthy as the gRPC port's
exposure: anyone who can reach that port can set them. Pair it with
`AuthUnaryInterceptor` on the gRPC server, or use the interceptor alone. The
gateway forwards the `Authorization` header, so the interceptor sees the token
either way. Permissions are not forwarded in headers.

## Why use this package?

- Reduces code duplication between REST and gRPC layers
- Ensures consistent traceability and error handling
- Simplifies context and metadata management

---
MIT License
