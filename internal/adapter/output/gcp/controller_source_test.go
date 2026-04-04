package gcp_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// extractMethodBody reads controller.go and extracts the body of the named method.
func extractMethodBody(t *testing.T, methodSignature string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	controllerPath := filepath.Join(filepath.Dir(file), "controller.go")
	content, err := os.ReadFile(controllerPath)
	if err != nil {
		t.Fatalf("failed to read controller.go: %v", err)
	}
	src := string(content)
	startIdx := strings.Index(src, methodSignature)
	if startIdx == -1 {
		t.Fatalf("method %q not found in controller.go", methodSignature)
	}
	endIdx := strings.Index(src[startIdx+1:], "\nfunc ")
	if endIdx == -1 {
		return src[startIdx:]
	}
	return src[startIdx : startIdx+1+endIdx]
}

func TestShiftTraffic_GetServiceBeforeUpdate(t *testing.T) {
	body := extractMethodBody(t, "func (c *Controller) ShiftTraffic(")

	getIdx := strings.Index(body, ".GetService(")
	updateIdx := strings.Index(body, ".UpdateService(")
	if getIdx == -1 {
		t.Error("ShiftTraffic must call client.GetService")
	}
	if updateIdx == -1 {
		t.Error("ShiftTraffic must call client.UpdateService")
	}
	if getIdx != -1 && updateIdx != -1 && getIdx >= updateIdx {
		t.Error("ShiftTraffic must call GetService BEFORE UpdateService")
	}
}

func TestShiftTraffic_UsesIdempotencyCheck(t *testing.T) {
	body := extractMethodBody(t, "func (c *Controller) ShiftTraffic(")

	if !strings.Contains(body, "isTrafficAlreadyMatching(") {
		t.Error("ShiftTraffic must call isTrafficAlreadyMatching for idempotent behavior")
	}
	if !strings.Contains(body, "selectActiveRevision(") {
		t.Error("ShiftTraffic must call selectActiveRevision to pick highest-traffic revision")
	}
}

func TestUpdateWorkerPool_GetWorkerPoolBeforeUpdate(t *testing.T) {
	body := extractMethodBody(t, "func (c *Controller) UpdateWorkerPool(")

	getIdx := strings.Index(body, ".GetWorkerPool(")
	updateIdx := strings.Index(body, ".UpdateWorkerPool(")
	if getIdx == -1 {
		t.Error("UpdateWorkerPool must call client.GetWorkerPool")
	}
	if updateIdx == -1 {
		t.Error("UpdateWorkerPool must call client.UpdateWorkerPool")
	}
	if getIdx != -1 && updateIdx != -1 && getIdx >= updateIdx {
		t.Error("UpdateWorkerPool must call GetWorkerPool BEFORE UpdateWorkerPool")
	}
}

func TestUpdateWorkerPool_UsesExplicitRevisions(t *testing.T) {
	body := extractMethodBody(t, "func (c *Controller) UpdateWorkerPool(")

	if strings.Contains(body, "INSTANCE_SPLIT_ALLOCATION_TYPE_LATEST") {
		t.Error("UpdateWorkerPool must NOT use LATEST — use explicit revision via selectActiveRevision")
	}
	if !strings.Contains(body, "selectActiveRevision(") {
		t.Error("UpdateWorkerPool must call selectActiveRevision to pick active revision by traffic")
	}
	if !strings.Contains(body, "isTrafficAlreadyMatching(") {
		t.Error("UpdateWorkerPool must call isTrafficAlreadyMatching for idempotent behavior")
	}
}
