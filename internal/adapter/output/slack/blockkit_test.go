package slack

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

func TestEnvironmentImageURL_Production(t *testing.T) {
	// given
	env := "production"

	// when
	url := EnvironmentImageURL(env)

	// then
	if !strings.Contains(url, "FF0000") {
		t.Errorf("expected production URL to contain FF0000, got %s", url)
	}
}

func TestEnvironmentImageURL_Staging(t *testing.T) {
	// given
	env := "staging"

	// when
	url := EnvironmentImageURL(env)

	// then
	if !strings.Contains(url, "FFA500") {
		t.Errorf("expected staging URL to contain FFA500, got %s", url)
	}
}

func TestEnvironmentImageURL_Development(t *testing.T) {
	// given
	env := "development"

	// when
	url := EnvironmentImageURL(env)

	// then
	if !strings.Contains(url, "008000") {
		t.Errorf("expected development URL to contain 008000, got %s", url)
	}
}

func TestEnvironmentImageURL_Unknown(t *testing.T) {
	// given
	env := "unknown-env"

	// when
	url := EnvironmentImageURL(env)

	// then
	if url != DefaultEnvironmentImage {
		t.Errorf("expected default image URL for unknown env, got %s", url)
	}
	if !strings.Contains(url, "808080") {
		t.Errorf("expected default URL to contain 808080 (gray), got %s", url)
	}
}

func TestBuildApprovalMessage_ContainsApproveButton(t *testing.T) {
	// given
	p := DeploymentPayload{
		Environment:  "staging",
		ResourceType: "service",
		ResourceName: "frontend-service",
		Target:       "v2",
		Action:       "canary_10",
		BuildInfo:    "main @ a1b2c3d",
		IssuedAt:     time.Now(),
		ApproveValue: `{"action":"approve"}`,
		DenyValue:    `{"action":"deny"}`,
	}

	// when
	msg := BuildApprovalMessage(p)

	// then
	blocks, ok := msg["blocks"].([]map[string]any)
	if !ok {
		t.Fatal("expected blocks to be []map[string]any")
	}

	found := false
	for _, block := range blocks {
		if block["type"] == "actions" {
			elements, ok := block["elements"].([]map[string]any)
			if !ok {
				t.Fatal("expected elements to be []map[string]any")
			}
			for _, el := range elements {
				if el["action_id"] == "approve" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("expected approve button with action_id='approve' in actions block")
	}
}

func TestBuildApprovalMessage_ContainsDenyButton(t *testing.T) {
	// given
	p := DeploymentPayload{
		Environment:  "staging",
		ResourceType: "service",
		ResourceName: "frontend-service",
		Target:       "v2",
		Action:       "canary_10",
		BuildInfo:    "main @ a1b2c3d",
		IssuedAt:     time.Now(),
		ApproveValue: `{"action":"approve"}`,
		DenyValue:    `{"action":"deny"}`,
	}

	// when
	msg := BuildApprovalMessage(p)

	// then
	blocks, ok := msg["blocks"].([]map[string]any)
	if !ok {
		t.Fatal("expected blocks to be []map[string]any")
	}

	found := false
	for _, block := range blocks {
		if block["type"] == "actions" {
			elements, ok := block["elements"].([]map[string]any)
			if !ok {
				t.Fatal("expected elements to be []map[string]any")
			}
			for _, el := range elements {
				if el["action_id"] == "deny" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("expected deny button with action_id='deny' in actions block")
	}
}

func TestBuildApprovalMessage_NoActionsInCompletion(t *testing.T) {
	// given
	approverID := "U12345"
	summary := "deployed frontend-service"
	env := "production"

	// when
	msg := BuildCompletionMessage(approverID, summary, env)

	// then
	blocks, ok := msg["blocks"].([]map[string]any)
	if !ok {
		t.Fatal("expected blocks to be []map[string]any")
	}

	for _, block := range blocks {
		if block["type"] == "actions" {
			t.Error("BuildCompletionMessage must not contain an 'actions' block")
		}
	}
}

func TestBuildApprovalMessage_ProductionImage(t *testing.T) {
	// given
	p := DeploymentPayload{
		Environment:  "production",
		ResourceType: "service",
		ResourceName: "frontend-service",
		Target:       "v3",
		Action:       "canary_50",
		BuildInfo:    "main @ deadbeef",
		IssuedAt:     time.Now(),
		ApproveValue: `{"action":"approve"}`,
		DenyValue:    `{"action":"deny"}`,
	}

	// when
	msg := BuildApprovalMessage(p)

	// then
	blocks, ok := msg["blocks"].([]map[string]any)
	if !ok {
		t.Fatal("expected blocks to be []map[string]any")
	}

	found := false
	for _, block := range blocks {
		if block["type"] == "section" {
			accessory, ok := block["accessory"].(map[string]any)
			if !ok {
				continue
			}
			imageURL, ok := accessory["image_url"].(string)
			if !ok {
				continue
			}
			if strings.Contains(imageURL, "FF0000") {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected production section accessory image URL to contain FF0000")
	}
}

func TestBuildApprovalMessage_RequireConfirm_ApproveButtonHasConfirmObject(t *testing.T) {
	// given
	p := DeploymentPayload{
		Environment:    "production",
		ResourceType:   "service",
		ResourceName:   "frontend-service",
		Target:         "v2",
		Action:         "canary_10",
		BuildInfo:      "main @ abc1234",
		IssuedAt:       time.Now(),
		ApproveValue:   `{"action":"approve","migration_done":false}`,
		DenyValue:      `{"action":"deny"}`,
		RequireConfirm: true,
	}

	// when
	msg := BuildApprovalMessage(p)

	// then — approve button must have a confirm object
	blocks, ok := msg["blocks"].([]map[string]any)
	if !ok {
		t.Fatal("expected blocks to be []map[string]any")
	}
	found := false
	for _, block := range blocks {
		if block["type"] != "actions" {
			continue
		}
		elements, ok := block["elements"].([]map[string]any)
		if !ok {
			continue
		}
		for _, el := range elements {
			if el["action_id"] == "approve" {
				if _, hasConfirm := el["confirm"]; hasConfirm {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("expected approve button to have a 'confirm' object when RequireConfirm=true")
	}
}

func TestBuildApprovalMessage_NoRequireConfirm_ApproveButtonHasNoConfirmObject(t *testing.T) {
	// given
	p := DeploymentPayload{
		Environment:    "production",
		ResourceType:   "service",
		ResourceName:   "frontend-service",
		Target:         "v2",
		Action:         "canary_10",
		BuildInfo:      "main @ abc1234",
		IssuedAt:       time.Now(),
		ApproveValue:   `{"action":"approve"}`,
		DenyValue:      `{"action":"deny"}`,
		RequireConfirm: false,
	}

	// when
	msg := BuildApprovalMessage(p)

	// then — approve button must NOT have a confirm object
	blocks, ok := msg["blocks"].([]map[string]any)
	if !ok {
		t.Fatal("expected blocks to be []map[string]any")
	}
	for _, block := range blocks {
		if block["type"] != "actions" {
			continue
		}
		elements, ok := block["elements"].([]map[string]any)
		if !ok {
			continue
		}
		for _, el := range elements {
			if el["action_id"] == "approve" {
				if _, hasConfirm := el["confirm"]; hasConfirm {
					t.Error("expected no 'confirm' object on approve button when RequireConfirm=false")
				}
			}
		}
	}
}

func TestBuildProgressMessage_WithNextAndStop_ContainsBothButtons(t *testing.T) {
	// given
	nextReq := &domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "v2",
		Action:        "canary_30",
		IssuedAt:      1700000000,
	}
	stopReq := &domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "v2",
		Action:        "rollback",
		IssuedAt:      1700000000,
	}

	// when
	msg := BuildProgressMessage("✅ 10% 完了", nextReq, stopReq)

	// then
	blocks, ok := msg["blocks"].([]map[string]any)
	if !ok {
		t.Fatal("expected blocks to be []map[string]any")
	}
	var approveFound, stopFound bool
	for _, block := range blocks {
		if block["type"] != "actions" {
			continue
		}
		elements, ok := block["elements"].([]map[string]any)
		if !ok {
			continue
		}
		for _, el := range elements {
			if el["action_id"] == "approve" {
				approveFound = true
			}
			if el["action_id"] == "approve" && el["style"] == "danger" {
				stopFound = true
			}
		}
		// stop button uses action_id="approve" (rollback-as-approval) with danger style
		for _, el := range elements {
			if el["action_id"] == "approve" {
				text, _ := el["text"].(map[string]any)
				if label, _ := text["text"].(string); strings.Contains(label, "停止") {
					stopFound = true
				}
			}
		}
	}
	if !approveFound {
		t.Error("expected advance button (action_id=approve) in progress message")
	}
	if !stopFound {
		t.Error("expected stop/rollback button in progress message")
	}
}

func TestBuildProgressMessage_NilNextReq_NoActionsBlock(t *testing.T) {
	// given — no next step; message should have no buttons
	msg := BuildProgressMessage("✅ 100% 完了", nil, nil)

	// then
	blocks, ok := msg["blocks"].([]map[string]any)
	if !ok {
		t.Fatal("expected blocks to be []map[string]any")
	}
	for _, block := range blocks {
		if block["type"] == "actions" {
			t.Error("expected no actions block when nextReq is nil")
		}
	}
}

func TestSafeTrunc_ShortString_Unchanged(t *testing.T) {
	// given
	s := "hello"

	// when
	got := safeTrunc(s, 10)

	// then
	if got != s {
		t.Errorf("expected unchanged string %q, got %q", s, got)
	}
}

func TestSafeTrunc_ExactLimit_Unchanged(t *testing.T) {
	// given
	s := "hello"

	// when
	got := safeTrunc(s, 5)

	// then
	if got != s {
		t.Errorf("expected unchanged string %q, got %q", s, got)
	}
}

func TestSafeTrunc_OverLimit_TruncatedWithEllipsis(t *testing.T) {
	// given
	s := "hello world"

	// when
	got := safeTrunc(s, 5)

	// then
	if len([]rune(got)) != 5 {
		t.Errorf("expected 5 runes, got %d: %q", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected truncated string to end with '…', got %q", got)
	}
}

func TestSafeTrunc_MultibyteSafe(t *testing.T) {
	// given — 10 Japanese characters (each is a multibyte rune)
	s := "あいうえおかきくけこ"

	// when
	got := safeTrunc(s, 5)

	// then — must be 5 runes, not 5 bytes
	runes := []rune(got)
	if len(runes) != 5 {
		t.Errorf("expected 5 runes, got %d: %q", len(runes), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected '…' suffix, got %q", got)
	}
}

func TestBuildApprovalMessage_LongResourceName_SectionTextWithinLimit(t *testing.T) {
	// given — resource name and target that together are 1200 chars (well over reasonable but under 3000)
	longName := strings.Repeat("a", 600)
	longTarget := strings.Repeat("b", 600)
	p := DeploymentPayload{
		Environment:  "production",
		ResourceType: "service",
		ResourceName: longName,
		Target:       longTarget,
		Action:       "canary_10",
		BuildInfo:    "main @ abc1234",
		IssuedAt:     time.Now(),
		ApproveValue: `{"action":"approve"}`,
		DenyValue:    `{"action":"deny"}`,
	}

	// when
	msg := BuildApprovalMessage(p)

	// then — the section text must be ≤ maxSectionText runes
	blocks, ok := msg["blocks"].([]map[string]any)
	if !ok {
		t.Fatal("expected blocks to be []map[string]any")
	}
	for _, block := range blocks {
		if block["type"] != "section" {
			continue
		}
		textObj, ok := block["text"].(map[string]any)
		if !ok {
			continue
		}
		text, ok := textObj["text"].(string)
		if !ok {
			continue
		}
		if len([]rune(text)) > maxSectionText {
			t.Errorf("section text exceeds maxSectionText (%d): got %d runes", maxSectionText, len([]rune(text)))
		}
	}
}

func TestCompressButtonValue_AlwaysGzPrefix(t *testing.T) {
	// Compression is unconditional — even a short value must be gz: prefixed.
	s := `{"resource_type":"service","resource_names":"svc","action":"canary_10","issued_at":1700000000}`

	got := compressButtonValue(s)

	if !strings.HasPrefix(got, "gz:") {
		t.Errorf("expected compressed value to always start with 'gz:', got %q", got[:min(20, len(got))])
	}
}

func TestCompressButtonValue_Roundtrip(t *testing.T) {
	// Compress then manually decompress must return the original.
	import_b64 := func(s string) []byte {
		b, _ := base64.RawURLEncoding.DecodeString(s)
		return b
	}
	original := `{"resource_type":"service","resource_names":"frontend,backend","targets":"rev-001,rev-002","action":"canary_10","issued_at":1700000000,"migration_done":false}`

	compressed := compressButtonValue(original)
	if !strings.HasPrefix(compressed, "gz:") {
		t.Fatalf("expected gz: prefix, got %q", compressed[:min(20, len(compressed))])
	}
	raw := import_b64(compressed[3:])
	r, _ := gzip.NewReader(bytes.NewReader(raw))
	expanded, _ := io.ReadAll(r)
	if string(expanded) != original {
		t.Errorf("roundtrip mismatch: got %q", expanded)
	}
}

func TestMarshalActionValue_AlwaysGzPrefix(t *testing.T) {
	// Even a minimal single-service request must produce a gz: compressed value.
	req := &domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "frontend-service-00001-abc",
		Action:        "canary_10",
		IssuedAt:      1700000000,
	}

	val := marshalActionValue(req)

	if !strings.HasPrefix(val, "gz:") {
		t.Errorf("expected gz: prefix for any bundle size, got %q", val[:min(20, len(val))])
	}
	if len(val) > maxButtonValue {
		t.Errorf("compressed single-service value (%d) exceeds maxButtonValue (%d)", len(val), maxButtonValue)
	}
}

func TestMarshalActionValue_LargeBundle_RoundtripDecodesCorrectly(t *testing.T) {
	// given — 10 services with long names
	names := strings.Join([]string{
		"very-long-service-name-frontend-001",
		"very-long-service-name-backend-002",
		"very-long-service-name-worker-003",
		"very-long-service-name-api-gw-004",
		"very-long-service-name-auth-svc-005",
	}, ",")
	revs := strings.Join([]string{
		"very-long-service-name-frontend-001-00010-abc",
		"very-long-service-name-backend-002-00010-def",
		"very-long-service-name-worker-003-00010-ghi",
		"very-long-service-name-api-gw-004-00010-jkl",
		"very-long-service-name-auth-svc-005-00010-mno",
	}, ",")
	req := &domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: names,
		Targets:       revs,
		Action:        "canary_10",
		IssuedAt:      1700000000,
	}

	// when
	val := marshalActionValue(req)

	// then — result is gz: prefixed and decodes to valid JSON preserving field values
	if !strings.HasPrefix(val, "gz:") {
		t.Fatalf("expected gz: prefix, got %q", val[:min(20, len(val))])
	}
	raw, err := base64.RawURLEncoding.DecodeString(val[3:])
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	r, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	expanded, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("gzip read: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(expanded, &out); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if got := out["resource_names"]; got != names {
		t.Errorf("resource_names: got %v, want %v", got, names)
	}
}

func TestMarshalActionValue_IncludesProjectAndLocation(t *testing.T) {
	// given
	req := &domain.ApprovalRequest{
		Project:       "my-gcp-project",
		Location:      "us-central1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "v2",
		Action:        "canary_10",
		IssuedAt:      1700000000,
	}

	// when
	val := marshalActionValue(req)

	// then — decode gz: prefix, base64url decode, gzip decompress, json unmarshal
	if !strings.HasPrefix(val, "gz:") {
		t.Fatalf("expected gz: prefix, got %q", val[:min(20, len(val))])
	}
	raw, err := base64.RawURLEncoding.DecodeString(val[3:])
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	r, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	expanded, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("gzip read: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(expanded, &out); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if got := out["project"]; got != "my-gcp-project" {
		t.Errorf("project: got %v, want my-gcp-project", got)
	}
	if got := out["location"]; got != "us-central1" {
		t.Errorf("location: got %v, want us-central1", got)
	}
}

func TestBuildProgressMessage_StopReqNonRollback_UsesDenyActionID(t *testing.T) {
	// given — stopReq with action != "rollback" must produce a deny button (action_id="deny")
	nextReq := &domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "v2",
		Action:        "canary_30",
		IssuedAt:      1700000000,
	}
	stopReq := &domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "v2",
		Action:        "canary_10", // not "rollback"
		IssuedAt:      1700000000,
	}

	// when
	msg := BuildProgressMessage("✅ 10% 完了", nextReq, stopReq)

	// then — stop button must use action_id="deny" (not "approve") for non-rollback action
	blocks, ok := msg["blocks"].([]map[string]any)
	if !ok {
		t.Fatal("expected blocks to be []map[string]any")
	}
	var denyFound bool
	for _, block := range blocks {
		if block["type"] != "actions" {
			continue
		}
		elements, ok := block["elements"].([]map[string]any)
		if !ok {
			continue
		}
		for _, el := range elements {
			if el["action_id"] == "deny" {
				denyFound = true
			}
		}
	}
	if !denyFound {
		t.Error("expected stop button with action_id='deny' for non-rollback stopReq")
	}
}

func TestCanaryBtnLabel_ZeroPercent_DefaultLabel(t *testing.T) {
	// given — canary_0 parses to percent=0; label must fall back to "✅ Canary"
	req := &domain.ApprovalRequest{
		Action: "canary_0",
	}

	// when
	label := canaryBtnLabel(req)

	// then
	if label != "✅ Canary" {
		t.Errorf("expected '✅ Canary' for canary_0, got %q", label)
	}
}

func TestBuildDenialMessage_ContainsDenierID(t *testing.T) {
	// given
	denierID := "U99999"
	summary := "denied deployment of backend-service"

	// when
	msg := BuildDenialMessage(denierID, summary)

	// then
	blocks, ok := msg["blocks"].([]map[string]any)
	if !ok {
		t.Fatal("expected blocks to be []map[string]any")
	}

	found := false
	for _, block := range blocks {
		if block["type"] == "section" {
			textBlock, ok := block["text"].(map[string]any)
			if !ok {
				continue
			}
			text, ok := textBlock["text"].(string)
			if !ok {
				continue
			}
			if strings.Contains(text, denierID) {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected denial message to contain denier ID %s", denierID)
	}
}
