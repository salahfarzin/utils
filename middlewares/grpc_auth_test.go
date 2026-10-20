package middlewares

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// fakeAuth accepts the token "good" as admin with orders view/edit.
func fakeAuth(token string) (*User, error) {
	if token != "good" {
		return nil, errors.New("invalid")
	}
	return &User{ID: "7", Uuid: "u-1", Roles: []string{"admin"},
		Permissions: Permissions{"orders": {"view", "edit"}}}, nil
}

func incoming(pairs ...string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(pairs...))
}

// callUnary runs the interceptor and returns the user and metadata the handler
// saw, and whether it ran.
func callUnary(t *testing.T, ctx context.Context, method string, opts ...GRPCAuthOption) (*User, metadata.MD, bool, error) {
	t.Helper()
	var seen *User
	var md metadata.MD
	ran := false
	_, err := AuthUnaryInterceptor(fakeAuth, opts...)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method},
		func(ctx context.Context, _ any) (any, error) {
			ran = true
			seen, _ = GetUser(ctx)
			md, _ = metadata.FromIncomingContext(ctx)
			return nil, nil
		})
	return seen, md, ran, err
}

const ordersList = "/orders.OrderService/List"

func TestAuthUnaryInterceptor(t *testing.T) {
	t.Run("puts the caller from a bearer token in the context", unaryBearerToken)

	t.Run("accepts the access_token cookie the gateway forwards", unaryCookieToken)

	t.Run("refuses before the handler", unaryRefusals)

	t.Run("drops forged x-user metadata, even on public methods", unaryDropsForgedMetadata)

	t.Run("public methods still see a caller who sends a token", unaryPublicWithToken)

	t.Run("a public service prefix needs its trailing slash", unaryPublicPrefix)

	t.Run("requires the permission listed for the method", unaryRequiredPermissions)
}

func unaryBearerToken(t *testing.T) {
	user, _, ran, err := callUnary(t, incoming("authorization", "Bearer good"), ordersList)
	if err != nil || !ran || user == nil || user.Uuid != "u-1" {
		t.Fatalf("user %+v ran %v err %v", user, ran, err)
	}
	if got := GetUserFromContext(context.WithValue(context.Background(), UserKey, user)); got.Uuid != "u-1" {
		t.Fatalf("GetUserFromContext = %+v", got)
	}
}

func unaryCookieToken(t *testing.T) {
	user, _, _, err := callUnary(t, incoming("grpcgateway-cookie", "theme=dark; access_token=good"), ordersList)
	if err != nil || user == nil {
		t.Fatalf("user %+v err %v", user, err)
	}
}

func unaryRefusals(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"no token":       context.Background(),
		"invalid token":  incoming("authorization", "Bearer bad"),
		"not bearer":     incoming("authorization", "Basic good"),
		"empty cookie":   incoming("grpcgateway-cookie", "access_token="),
		"forged headers": incoming("x-user-uuid", "u-1", "x-user-roles", "super-admin"),
	} {
		_, _, ran, err := callUnary(t, ctx, ordersList)
		if status.Code(err) != codes.Unauthenticated || ran {
			t.Errorf("%s: code %v ran %v", name, status.Code(err), ran)
		}
	}
}

func unaryDropsForgedMetadata(t *testing.T) {
	ctx := incoming("x-user-uuid", "victim", "x-user-roles", "super-admin", "x-trace-id", "t1")
	user, md, ran, err := callUnary(t, ctx, "/health.Health/Check", PublicMethods("/health.Health/"))
	if err != nil || !ran || user != nil {
		t.Fatalf("user %+v ran %v err %v", user, ran, err)
	}
	if len(md.Get("x-user-uuid")) > 0 || len(md.Get("x-user-roles")) > 0 {
		t.Fatalf("forged metadata reached the handler: %v", md)
	}
	if got := md.Get("x-trace-id"); len(got) != 1 {
		t.Fatalf("other metadata was dropped too: %v", md)
	}
	if got := GetUserFromContext(metadata.NewIncomingContext(context.Background(), md)); got.Uuid != "" {
		t.Fatalf("GetUserFromContext still trusts forged headers: %+v", got)
	}
}

func unaryPublicWithToken(t *testing.T) {
	user, _, _, err := callUnary(t, incoming("authorization", "Bearer good"), "/auth.Public/Info",
		PublicMethods("/auth.Public/Info"))
	if err != nil || user == nil {
		t.Fatalf("user %+v err %v", user, err)
	}
	_, _, ran, err := callUnary(t, incoming("authorization", "Bearer bad"), "/auth.Public/Info",
		PublicMethods("/auth.Public/Info"))
	if err != nil || !ran {
		t.Fatalf("a bad token must not close a public method: ran %v err %v", ran, err)
	}
}

func unaryPublicPrefix(t *testing.T) {
	_, _, _, err := callUnary(t, context.Background(), "/health.HealthX/Check", PublicMethods("/health.Health/"))
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("/health.HealthX matched /health.Health/: %v", err)
	}
}

func unaryRequiredPermissions(t *testing.T) {
	required := RequirePermissions(map[string]Permission{
		ordersList:                  {"orders", "view"},
		"/orders.OrderService/Drop": {"orders", "delete"},
	})
	ok := incoming("authorization", "Bearer good")
	if _, _, ran, err := callUnary(t, ok, ordersList, required); err != nil || !ran {
		t.Fatalf("held permission refused: %v", err)
	}
	if _, _, ran, err := callUnary(t, ok, "/orders.OrderService/Drop", required); status.Code(err) != codes.PermissionDenied || ran {
		t.Fatalf("missing permission: code %v ran %v", status.Code(err), ran)
	}
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s fakeStream) Context() context.Context { return s.ctx }

func TestAuthStreamInterceptor(t *testing.T) {
	interceptor := AuthStreamInterceptor(fakeAuth)
	info := &grpc.StreamServerInfo{FullMethod: "/orders.OrderService/Watch"}

	t.Run("hands the handler a stream carrying the caller", func(t *testing.T) {
		var seen *User
		err := interceptor(nil, fakeStream{ctx: incoming("authorization", "Bearer good")}, info,
			func(_ any, ss grpc.ServerStream) error {
				seen, _ = GetUser(ss.Context())
				return nil
			})
		if err != nil || seen == nil || seen.Uuid != "u-1" {
			t.Fatalf("seen %+v err %v", seen, err)
		}
	})

	t.Run("refuses a stream without a token", func(t *testing.T) {
		err := interceptor(nil, fakeStream{ctx: context.Background()}, info,
			func(any, grpc.ServerStream) error { t.Fatal("handler ran"); return nil })
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("code %v", status.Code(err))
		}
	})
}

func TestUserCan(t *testing.T) {
	u := &User{Permissions: Permissions{"orders": {"view"}}}
	cases := []struct {
		resource, action string
		want             bool
	}{
		{"orders", "view", true},
		{"orders", "edit", false},
		{"users", "view", false},
	}
	for _, c := range cases {
		if got := u.Can(c.resource, c.action); got != c.want {
			t.Errorf("Can(%s, %s) = %v", c.resource, c.action, got)
		}
	}
	var nobody *User
	if nobody.Can("orders", "view") {
		t.Error("a nil user can do nothing")
	}
}

func TestRequirePermission(t *testing.T) {
	ctx := context.WithValue(context.Background(), UserKey, &User{Permissions: Permissions{"orders": {"view"}}})
	if err := RequirePermission(ctx, "orders", "view"); err != nil {
		t.Errorf("held: %v", err)
	}
	if code := status.Code(RequirePermission(ctx, "orders", "delete")); code != codes.PermissionDenied {
		t.Errorf("not held: %v", code)
	}
	if code := status.Code(RequirePermission(context.Background(), "orders", "view")); code != codes.Unauthenticated {
		t.Errorf("no caller: %v", code)
	}
}

// meJSON is what auth's REST gateway returns for /auth/me (protojson, proto names).
const meJSON = `{"id":"7","uuid":"u-1","username":"sam","email":"sam@example.com","roles":["admin"],` +
	`"first_name":"Sam","permissions":{"orders":{"actions":["view","edit"]}}}`

// fakeAuthMe answers /api/v1/auth/me by bearer token. Only an exact
// "Bearer <token>" header matches, so a success also proves the client sent it.
func fakeAuthMe(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/auth/me" {
		http.NotFound(w, r)
		return
	}
	switch r.Header.Get("Authorization") {
	case "Bearer good":
		_, _ = w.Write([]byte(meJSON))
	case "Bearer legacy":
		_, _ = w.Write([]byte(`{"id":7,"uuid":"u-2","email":{"value":"old@example.com"}}`))
	case "Bearer broken":
		w.WriteHeader(http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusUnauthorized)
	}
}

func TestNewAuthMeFunc(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(fakeAuthMe))
	defer srv.Close()
	authenticate := NewAuthMeFunc(srv.URL+"/api/v1/", nil)

	t.Run("maps auth's response, permissions included", func(t *testing.T) {
		u, err := authenticate("good")
		if err != nil {
			t.Fatal(err)
		}
		checkMeUser(t, u)
	})

	t.Run("accepts wrapper objects and numeric ids", func(t *testing.T) {
		u, err := authenticate("legacy")
		if err != nil || u.ID != "7" || u.Email != "old@example.com" {
			t.Fatalf("user %+v err %v", u, err)
		}
	})

	t.Run("a rejected token is ErrUnauthenticated", func(t *testing.T) {
		if _, err := authenticate("bad"); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("an auth outage is not ErrUnauthenticated", func(t *testing.T) {
		if _, err := authenticate("broken"); err == nil || errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("err %v", err)
		}
	})
}

// checkMeUser checks u is meJSON's user.
func checkMeUser(t *testing.T, u *User) {
	t.Helper()
	if u.ID != "7" || u.Uuid != "u-1" || u.Email != "sam@example.com" || len(u.Roles) != 1 {
		t.Errorf("user %+v", u)
	}
	if u.FirstName == nil || *u.FirstName != "Sam" || u.LastName != nil {
		t.Errorf("names %v %v", u.FirstName, u.LastName)
	}
	if !u.Can("orders", "edit") || u.Can("orders", "delete") {
		t.Errorf("permissions %v", u.Permissions)
	}
}
