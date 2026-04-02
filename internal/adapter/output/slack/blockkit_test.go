package slack

import (
	"strings"
	"testing"
	"time"
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
