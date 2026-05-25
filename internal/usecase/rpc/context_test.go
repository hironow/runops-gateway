package rpc_test

import (
	"context"
	"testing"

	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
)

func TestOperatorFromContext_RoundTrip(t *testing.T) {
	// given
	op, _ := domainrpc.NewOperator("U01234ABCD", "alice@example.com")
	ctx := usecaserpc.WithOperator(context.Background(), op)

	// when
	got, ok := usecaserpc.OperatorFromContext(ctx)

	// then
	if !ok {
		t.Fatalf("expected operator present in context")
	}
	if got != op {
		t.Errorf("operator: got %+v, want %+v", got, op)
	}
}

func TestOperatorFromContext_Absent_ReturnsZero(t *testing.T) {
	// given - bare context with no operator carried
	ctx := context.Background()

	// when
	op, ok := usecaserpc.OperatorFromContext(ctx)

	// then
	if ok {
		t.Errorf("expected no operator, got %+v", op)
	}
	if !op.IsZero() {
		t.Errorf("returned Operator must be zero on miss, got %+v", op)
	}
}

func TestWithOperator_DoesNotMutateInputContext(t *testing.T) {
	// given
	op, _ := domainrpc.NewOperator("U1", "a@b.c")
	parent := context.Background()

	// when
	child := usecaserpc.WithOperator(parent, op)

	// then - parent must not see the value
	if _, ok := usecaserpc.OperatorFromContext(parent); ok {
		t.Errorf("parent context unexpectedly carries operator")
	}
	// child must see it
	if _, ok := usecaserpc.OperatorFromContext(child); !ok {
		t.Errorf("child context missing operator")
	}
}
