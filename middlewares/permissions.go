package middlewares

import (
	"context"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Permissions maps a resource to the actions the user may take on it, e.g.
// {"orders": ["view", "edit"]}. auth's /auth/me returns them: the union of the
// grants of the user's roles. The vocabulary belongs to each product.
type Permissions map[string][]string

// Permission is one action on one resource.
type Permission struct {
	Resource string
	Action   string
}

// Can reports whether the user may take action on resource. Check this rather
// than role names: roles are configurable, their grants are what count.
func (u *User) Can(resource, action string) bool {
	return u != nil && slices.Contains(u.Permissions[resource], action)
}

// RequirePermission checks the caller in ctx, as an interceptor or middleware
// left it, may take action on resource. The error is a gRPC status:
// Unauthenticated without a caller, PermissionDenied without the permission.
func RequirePermission(ctx context.Context, resource, action string) error {
	user, ok := GetUser(ctx)
	if !ok || user == nil {
		return status.Error(codes.Unauthenticated, "missing or invalid token")
	}
	if !user.Can(resource, action) {
		return status.Errorf(codes.PermissionDenied, "requires %s on %s", action, resource)
	}
	return nil
}
