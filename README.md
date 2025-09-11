
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

## Why use this package?

- Reduces code duplication between REST and gRPC layers
- Ensures consistent traceability and error handling
- Simplifies context and metadata management

---
MIT License
