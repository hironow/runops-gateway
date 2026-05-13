package main

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestNewRPCReadiness_PopulatesAllFields(t *testing.T) {
	// when
	r := newRPCReadiness(true, true, true, true, true)

	// then
	if !r.EndpointEnabled || !r.HighMutationEnabled || !r.RegistryLoaded ||
		!r.AdminApprovalProducerEnabled || !r.AdminApprovalConsumerEnabled {
		t.Errorf("all flags should be true; got %+v", r)
	}
	if len(r.RegisteredMethods.High) == 0 || len(r.RegisteredMethods.Low) == 0 {
		t.Errorf("registered methods empty: high=%v low=%v",
			r.RegisteredMethods.High, r.RegisteredMethods.Low)
	}
}

func TestNewRPCReadiness_EnumeratesKnownMethods(t *testing.T) {
	// then - the classifier must register at least these well-known
	// methods so the readiness signal is meaningful to operators.
	r := newRPCReadiness(false, false, false, false, false)

	expectedHigh := []string{
		"runops.admin.project.add",
		"runops.admin.project.archive",
	}
	for _, name := range expectedHigh {
		if !slices.Contains(r.RegisteredMethods.High, name) {
			t.Errorf("HIGH method missing: %q (got %v)", name, r.RegisteredMethods.High)
		}
	}
	expectedLow := []string{
		"runops.admin.project.get",
		"runops.admin.project.list",
		"runops.admin.project.pending.get",
	}
	for _, name := range expectedLow {
		if !slices.Contains(r.RegisteredMethods.Low, name) {
			t.Errorf("LOW method missing: %q (got %v)", name, r.RegisteredMethods.Low)
		}
	}
}

func TestMarshalReadiness_TopLevelStatusStaysOk(t *testing.T) {
	// given
	r := newRPCReadiness(false, false, false, false, false)

	// when
	out, err := marshalReadiness(r)
	if err != nil {
		t.Fatalf("marshalReadiness: %v", err)
	}

	// then
	var probe map[string]any
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if probe["status"] != "ok" {
		t.Errorf("top-level status: got %v, want ok", probe["status"])
	}
	if _, ok := probe["adr_0040"]; !ok {
		t.Errorf("adr_0040 field missing in payload: %s", out)
	}
}

func TestMarshalReadiness_DefaultStateReportsFlagsOff(t *testing.T) {
	// given - empty wiring (= operator hasn't enabled anything)
	r := newRPCReadiness(false, false, false, false, false)

	// when
	out, _ := marshalReadiness(r)
	var p readinessPayload
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	// then
	if p.ADR0040.EndpointEnabled || p.ADR0040.HighMutationEnabled ||
		p.ADR0040.RegistryLoaded || p.ADR0040.AdminApprovalProducerEnabled ||
		p.ADR0040.AdminApprovalConsumerEnabled {
		t.Errorf("default state must be all-false; got %+v", p.ADR0040)
	}
}
