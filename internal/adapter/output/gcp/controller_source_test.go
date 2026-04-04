package gcp_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestShiftTraffic_GetServiceBeforeUpdate verifies that controller.go
// calls GetService before UpdateService in ShiftTraffic.
//
// Cloud Run API v2 requires the template field in UpdateServiceRequest.
// Without fetching the current service first, the update fails with
// "required field not present". This test prevents regressions by
// scanning the source code for the correct call order.
func TestShiftTraffic_GetServiceBeforeUpdate(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	controllerPath := filepath.Join(filepath.Dir(file), "controller.go")
	content, err := os.ReadFile(controllerPath)
	if err != nil {
		t.Fatalf("failed to read controller.go: %v", err)
	}

	src := string(content)

	// Extract the ShiftTraffic method body
	startIdx := strings.Index(src, "func (c *Controller) ShiftTraffic(")
	if startIdx == -1 {
		t.Fatal("ShiftTraffic method not found in controller.go")
	}

	// Find the next method boundary
	endIdx := strings.Index(src[startIdx+1:], "\nfunc ")
	var methodBody string
	if endIdx == -1 {
		methodBody = src[startIdx:]
	} else {
		methodBody = src[startIdx : startIdx+1+endIdx]
	}

	getIdx := strings.Index(methodBody, "client.GetService(")
	updateIdx := strings.Index(methodBody, "client.UpdateService(")

	if getIdx == -1 {
		t.Error("ShiftTraffic must call client.GetService to fetch current service state before updating")
	}
	if updateIdx == -1 {
		t.Error("ShiftTraffic must call client.UpdateService")
	}
	if getIdx != -1 && updateIdx != -1 && getIdx >= updateIdx {
		t.Error("ShiftTraffic must call GetService BEFORE UpdateService (Cloud Run API v2 requires template field)")
	}
}

// TestUpdateWorkerPool_GetWorkerPoolBeforeUpdate verifies that controller.go
// calls GetWorkerPool before UpdateWorkerPool.
func TestUpdateWorkerPool_GetWorkerPoolBeforeUpdate(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	controllerPath := filepath.Join(filepath.Dir(file), "controller.go")
	content, err := os.ReadFile(controllerPath)
	if err != nil {
		t.Fatalf("failed to read controller.go: %v", err)
	}

	src := string(content)

	startIdx := strings.Index(src, "func (c *Controller) UpdateWorkerPool(")
	if startIdx == -1 {
		t.Fatal("UpdateWorkerPool method not found in controller.go")
	}

	endIdx := strings.Index(src[startIdx+1:], "\nfunc ")
	var methodBody string
	if endIdx == -1 {
		methodBody = src[startIdx:]
	} else {
		methodBody = src[startIdx : startIdx+1+endIdx]
	}

	getIdx := strings.Index(methodBody, "client.GetWorkerPool(")
	updateIdx := strings.Index(methodBody, "client.UpdateWorkerPool(")

	if getIdx == -1 {
		t.Error("UpdateWorkerPool must call client.GetWorkerPool to fetch current state before updating")
	}
	if updateIdx == -1 {
		t.Error("UpdateWorkerPool must call client.UpdateWorkerPool")
	}
	if getIdx != -1 && updateIdx != -1 && getIdx >= updateIdx {
		t.Error("UpdateWorkerPool must call GetWorkerPool BEFORE UpdateWorkerPool (Cloud Run API v2 requires template field)")
	}
}
