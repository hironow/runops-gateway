package rpc_test

import (
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain/rpc"
)

func TestNewOperator_ValidFieldsAccepted(t *testing.T) {
	// given / when
	op, err := rpc.NewOperator("U01234ABCD", "alice@example.com")

	// then
	if err != nil {
		t.Fatalf("NewOperator failed: %v", err)
	}
	if op.OperatorID != "U01234ABCD" {
		t.Errorf("OperatorID: got %q", op.OperatorID)
	}
	if op.Email != "alice@example.com" {
		t.Errorf("Email: got %q", op.Email)
	}
	if op.ActorType != rpc.ActorTypeHumanOperator {
		t.Errorf("ActorType: got %q, want %q", op.ActorType, rpc.ActorTypeHumanOperator)
	}
}

func TestNewOperator_RejectsEmptyOperatorID(t *testing.T) {
	// given / when
	_, err := rpc.NewOperator("", "alice@example.com")

	// then
	if err == nil {
		t.Errorf("expected error for empty operator_id")
	}
}

func TestActorTypeHumanOperator_FixedValue(t *testing.T) {
	// then - per ADR 0040 §identity contract step 3:
	// requester_actor_type = "human-operator" 固定
	if rpc.ActorTypeHumanOperator != "human-operator" {
		t.Errorf("ActorTypeHumanOperator must be 'human-operator', got %q", rpc.ActorTypeHumanOperator)
	}
}

func TestOperator_IsZero(t *testing.T) {
	// given
	var zero rpc.Operator

	// then
	if !zero.IsZero() {
		t.Errorf("zero value Operator should report IsZero")
	}

	// given
	op, _ := rpc.NewOperator("U1", "a@b.c")

	// then
	if op.IsZero() {
		t.Errorf("populated Operator should not report IsZero")
	}
}
