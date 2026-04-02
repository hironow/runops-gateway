package slack

import (
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
		ResourceType: domain.ResourceTypeService,
		ResourceName: "frontend-service",
		Target:       "v2",
		Action:       "canary_30",
		IssuedAt:     1700000000,
	}
	stopReq := &domain.ApprovalRequest{
		ResourceType: domain.ResourceTypeService,
		ResourceName: "frontend-service",
		Target:       "v2",
		Action:       "rollback",
		IssuedAt:     1700000000,
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
