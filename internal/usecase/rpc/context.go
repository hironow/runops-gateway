package rpc

import (
	"context"

	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
)

// operatorCtxKey is unexported so external packages cannot inject Operator
// values without going through WithOperator (= type-safe binding).
type operatorCtxKey struct{}

// WithOperator returns a derived context carrying the authenticated Operator.
//
// The transport adapter (HTTP /rpc handler) populates this after a successful
// multi-token registry lookup so admin method handlers (= §B-4 / §B-5) can
// retrieve `effective_requester_id` without re-running the auth path.
func WithOperator(ctx context.Context, op domainrpc.Operator) context.Context {
	return context.WithValue(ctx, operatorCtxKey{}, op)
}

// OperatorFromContext returns the Operator carried in ctx, or the zero value
// and false if no Operator was injected. A returned `false` result MUST be
// treated by callers as "unauthenticated" — handlers that require operator
// identity should fail-closed in that case.
func OperatorFromContext(ctx context.Context) (domainrpc.Operator, bool) {
	op, ok := ctx.Value(operatorCtxKey{}).(domainrpc.Operator)
	if !ok {
		return domainrpc.Operator{}, false
	}
	return op, true
}
