package telemetry

import (
	"context"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

// ServerOptions instruments a gRPC server: a server span per call (named by
// its full method, continuing the caller's trace), the standard RPC server
// metrics, and a trace-aware logger in each call's context. Pass them to
// grpc.NewServer alongside your own options; interceptor chains add up, and
// these run first.
func ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(unaryTraceLogger),
		grpc.ChainStreamInterceptor(streamTraceLogger),
	}
}

// DialOptions instruments a gRPC client connection (including grpc-gateway's
// connection to its own gRPC server), so calls are client spans and carry
// the trace to the server.
func DialOptions() []grpc.DialOption {
	return []grpc.DialOption{grpc.WithStatsHandler(otelgrpc.NewClientHandler())}
}

func unaryTraceLogger(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	return handler(WithTraceLogger(ctx), req)
}

func streamTraceLogger(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	return handler(srv, &tracedStream{ServerStream: ss, ctx: WithTraceLogger(ss.Context())})
}

type tracedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *tracedStream) Context() context.Context { return s.ctx }
