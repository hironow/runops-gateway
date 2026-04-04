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
	url := EnvironmentImageURL("production")
	if !strings.Contains(url, "FF0000") {
		t.Errorf("expected production URL to contain FF0000, got %s", url)
	}
}

func TestEnvironmentImageURL_Staging(t *testing.T) {
	url := EnvironmentImageURL("staging")
	if !strings.Contains(url, "FFA500") {
		t.Errorf("expected staging URL to contain FFA500, got %s", url)
	}
}

func TestEnvironmentImageURL_Development(t *testing.T) {
	url := EnvironmentImageURL("development")
	if !strings.Contains(url, "008000") {
		t.Errorf("expected development URL to contain 008000, got %s", url)
	}
}

func TestEnvironmentImageURL_Unknown(t *testing.T) {
	url := EnvironmentImageURL("unknown-env")
	if url != DefaultEnvironmentImage {
		t.Errorf("expected default image URL for unknown env, got %s", url)
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
	found := false
	for _, block := range msg.Blocks {
		if block.Type != BlockTypeActions {
			continue
		}
		for _, el := range block.Elements {
			if el.ActionID == "approve" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected approve button with action_id='approve'")
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
	found := false
	for _, block := range msg.Blocks {
		if block.Type != BlockTypeActions {
			continue
		}
		for _, el := range block.Elements {
			if el.ActionID == "deny" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected deny button with action_id='deny'")
	}
}

func TestBuildCompletionMessage_NoActionsBlock(t *testing.T) {
	// given
	msg := BuildCompletionMessage("U12345", "deployed frontend-service", "production")

	// then
	for _, block := range msg.Blocks {
		if block.Type == BlockTypeActions {
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
	found := false
	for _, block := range msg.Blocks {
		if block.Type != BlockTypeSection || block.Accessory == nil {
			continue
		}
		if strings.Contains(block.Accessory.ImageURL, "FF0000") {
			found = true
		}
	}
	if !found {
		t.Error("expected production section accessory image URL to contain FF0000")
	}
}

func TestBuildApprovalMessage_RequireConfirm_HasConfirmDialog(t *testing.T) {
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

	// then
	found := false
	for _, block := range msg.Blocks {
		if block.Type != BlockTypeActions {
			continue
		}
		for _, el := range block.Elements {
			if el.ActionID == "approve" && el.Confirm != nil {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected approve button to have a confirm dialog when RequireConfirm=true")
	}
}

func TestBuildApprovalMessage_NoRequireConfirm_NoConfirmDialog(t *testing.T) {
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

	// then
	for _, block := range msg.Blocks {
		if block.Type != BlockTypeActions {
			continue
		}
		for _, el := range block.Elements {
			if el.ActionID == "approve" && el.Confirm != nil {
				t.Error("expected no confirm dialog when RequireConfirm=false")
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
	if !msg.ReplaceOriginal {
		t.Error("expected replace_original=true")
	}
	var approveFound, stopFound bool
	for _, block := range msg.Blocks {
		if block.Type != BlockTypeActions {
			continue
		}
		for _, el := range block.Elements {
			if el.ActionID == "approve" && el.Style == "primary" {
				approveFound = true
			}
			if el.Style == "danger" && strings.Contains(el.Text.Text, "停止") {
				stopFound = true
			}
		}
	}
	if !approveFound {
		t.Error("expected advance button (action_id=approve, style=primary)")
	}
	if !stopFound {
		t.Error("expected stop/rollback button (style=danger)")
	}
}

func TestBuildProgressMessage_NilNextReq_NoActionsBlock(t *testing.T) {
	msg := BuildProgressMessage("✅ 100% 完了", nil, nil)

	for _, block := range msg.Blocks {
		if block.Type == BlockTypeActions {
			t.Error("expected no actions block when nextReq is nil")
		}
	}
}

func TestSafeTrunc_ShortString_Unchanged(t *testing.T) {
	if got := safeTrunc("hello", 10); got != "hello" {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestSafeTrunc_ExactLimit_Unchanged(t *testing.T) {
	if got := safeTrunc("hello", 5); got != "hello" {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestSafeTrunc_OverLimit_TruncatedWithEllipsis(t *testing.T) {
	got := safeTrunc("hello world", 5)
	if len([]rune(got)) != 5 {
		t.Errorf("expected 5 runes, got %d: %q", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
}

func TestSafeTrunc_MultibyteSafe(t *testing.T) {
	got := safeTrunc("あいうえおかきくけこ", 5)
	runes := []rune(got)
	if len(runes) != 5 {
		t.Errorf("expected 5 runes, got %d: %q", len(runes), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
}

func TestBuildApprovalMessage_LongResourceName_SectionTextWithinLimit(t *testing.T) {
	// given
	p := DeploymentPayload{
		Environment:  "production",
		ResourceType: "service",
		ResourceName: strings.Repeat("a", 600),
		Target:       strings.Repeat("b", 600),
		Action:       "canary_10",
		BuildInfo:    "main @ abc1234",
		IssuedAt:     time.Now(),
		ApproveValue: `{"action":"approve"}`,
		DenyValue:    `{"action":"deny"}`,
	}

	// when
	msg := BuildApprovalMessage(p)

	// then
	for _, block := range msg.Blocks {
		if block.Type != BlockTypeSection || block.Text == nil {
			continue
		}
		if len([]rune(block.Text.Text)) > maxSectionText {
			t.Errorf("section text exceeds maxSectionText (%d): got %d runes",
				maxSectionText, len([]rune(block.Text.Text)))
		}
	}
}

func TestCompressButtonValue_AlwaysGzPrefix(t *testing.T) {
	s := `{"resource_type":"service","resource_names":"svc","action":"canary_10","issued_at":1700000000}`
	got := compressButtonValue(s)
	if !strings.HasPrefix(got, "gz:") {
		t.Errorf("expected gz: prefix, got %q", got[:min(20, len(got))])
	}
}

func TestCompressButtonValue_Roundtrip(t *testing.T) {
	original := `{"resource_type":"service","resource_names":"frontend,backend","targets":"rev-001,rev-002","action":"canary_10","issued_at":1700000000,"migration_done":false}`
	compressed := compressButtonValue(original)
	if !strings.HasPrefix(compressed, "gz:") {
		t.Fatalf("expected gz: prefix, got %q", compressed[:min(20, len(compressed))])
	}
	raw, _ := base64.RawURLEncoding.DecodeString(compressed[3:])
	r, _ := gzip.NewReader(bytes.NewReader(raw))
	expanded, _ := io.ReadAll(r)
	if string(expanded) != original {
		t.Errorf("roundtrip mismatch: got %q", expanded)
	}
}

func TestMarshalActionValue_AlwaysGzPrefix(t *testing.T) {
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
		t.Errorf("expected gz: prefix, got %q", val[:min(20, len(val))])
	}
	if len(val) > maxButtonValue {
		t.Errorf("compressed single-service value (%d) exceeds maxButtonValue (%d)", len(val), maxButtonValue)
	}
}

func TestMarshalActionValue_LargeBundle_RoundtripDecodesCorrectly(t *testing.T) {
	// given
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

	// then
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
		t.Errorf("resource_names mismatch")
	}
}

func TestMarshalActionValue_IncludesProjectAndLocation(t *testing.T) {
	req := &domain.ApprovalRequest{
		Project:       "my-gcp-project",
		Location:      "us-central1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "v2",
		Action:        "canary_10",
		IssuedAt:      1700000000,
	}
	val := marshalActionValue(req)
	raw, _ := base64.RawURLEncoding.DecodeString(val[3:])
	r, _ := gzip.NewReader(bytes.NewReader(raw))
	expanded, _ := io.ReadAll(r)
	var out map[string]any
	json.Unmarshal(expanded, &out)
	if got := out["project"]; got != "my-gcp-project" {
		t.Errorf("project: got %v", got)
	}
	if got := out["location"]; got != "us-central1" {
		t.Errorf("location: got %v", got)
	}
}

func TestBuildProgressMessage_StopReqNonRollback_UsesDenyActionID(t *testing.T) {
	// given — stopReq with action != "rollback" must produce deny button
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

	// then
	var denyFound bool
	for _, block := range msg.Blocks {
		if block.Type != BlockTypeActions {
			continue
		}
		for _, el := range block.Elements {
			if el.ActionID == "deny" {
				denyFound = true
			}
		}
	}
	if !denyFound {
		t.Error("expected stop button with action_id='deny' for non-rollback stopReq")
	}
}

func TestBuildProgressMessage_ActionIDsAreUnique(t *testing.T) {
	// Slack rejects actions blocks with duplicate action_ids.
	nextReq := &domain.ApprovalRequest{
		Project: "p", Location: "l", ResourceType: domain.ResourceTypeService,
		ResourceNames: "svc", Targets: "v2", Action: "canary_30", IssuedAt: 1700000000,
	}
	stopReq := &domain.ApprovalRequest{
		Project: "p", Location: "l", ResourceType: domain.ResourceTypeService,
		ResourceNames: "svc", Targets: "v2", Action: "rollback", IssuedAt: 1700000000,
	}

	msg := BuildProgressMessage("✅ 10%", nextReq, stopReq)

	for _, block := range msg.Blocks {
		if block.Type != BlockTypeActions {
			continue
		}
		seen := map[string]bool{}
		for _, el := range block.Elements {
			if seen[el.ActionID] {
				t.Errorf("duplicate action_id %q in actions block — Slack will reject this", el.ActionID)
			}
			seen[el.ActionID] = true
		}
	}
}

func TestCanaryBtnLabel_ZeroPercent_DefaultLabel(t *testing.T) {
	req := &domain.ApprovalRequest{Action: "canary_0"}
	if label := canaryBtnLabel(req); label != "✅ Canary" {
		t.Errorf("expected '✅ Canary', got %q", label)
	}
}

func TestBuildDenialMessage_ContainsDenierID(t *testing.T) {
	msg := BuildDenialMessage("U99999", "denied deployment of backend-service")

	found := false
	for _, block := range msg.Blocks {
		if block.Type == BlockTypeSection && block.Text != nil {
			if strings.Contains(block.Text.Text, "U99999") {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected denial message to contain denier ID")
	}
}

// --- Type safety tests: verify the typed payload prevents structural bugs ---

func TestBuildProgressMessage_ReplaceOriginalAlwaysTrue(t *testing.T) {
	// The old completionBlocks bug was that replace_original got nested inside blocks.
	// With typed SlackPayload, replace_original is a top-level field — nesting is impossible.
	msg := BuildProgressMessage("✅ test", nil, nil)
	if !msg.ReplaceOriginal {
		t.Error("replace_original must always be true in progress messages")
	}
}

func TestBuildApprovalMessage_BlockTypes(t *testing.T) {
	// Verify the approval message has the expected block structure:
	// header → section (with accessory) → divider → actions
	p := DeploymentPayload{
		Environment:  "staging",
		ResourceType: "service",
		ResourceName: "svc",
		Target:       "v1",
		Action:       "canary_10",
		BuildInfo:    "main",
		IssuedAt:     time.Now(),
		ApproveValue: "approve",
		DenyValue:    "deny",
	}
	msg := BuildApprovalMessage(p)

	expected := []BlockType{BlockTypeHeader, BlockTypeSection, BlockTypeDivider, BlockTypeActions}
	if len(msg.Blocks) != len(expected) {
		t.Fatalf("expected %d blocks, got %d", len(expected), len(msg.Blocks))
	}
	for i, block := range msg.Blocks {
		if block.Type != expected[i] {
			t.Errorf("block[%d].Type = %q, want %q", i, block.Type, expected[i])
		}
	}
}

func TestSlackPayload_JSONSerialization(t *testing.T) {
	// Verify that typed payload serializes to valid Slack JSON
	payload := ReplacePayload(SectionBlock("hello"))
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var raw map[string]any
	json.Unmarshal(b, &raw)

	if raw["replace_original"] != true {
		t.Error("replace_original should be true")
	}
	blocks, ok := raw["blocks"].([]any)
	if !ok || len(blocks) != 1 {
		t.Fatal("expected 1 block")
	}
	section := blocks[0].(map[string]any)
	if section["type"] != "section" {
		t.Errorf("type = %v, want section", section["type"])
	}
}
