package domain

import (
	"errors"
	"regexp"
	"time"
)

// Project is a multiplex registry entry. Shared by SQLite (dev/test) and
// Firestore (production, issue #0011) adapters via port.ProjectRegistry.
//
// Firestore tags map struct fields to document keys when the FirestoreProjectRegistry
// (#0011) writes Project values via firestore.DocumentRef.Create / DataTo. The
// SQLite adapter scans columns explicitly so the tags do not affect it.
type Project struct {
	ID                      string        `firestore:"id"                         json:"id"`
	GitHubOrg               string        `firestore:"github_org"                 json:"github_org"`
	GitHubRepo              string        `firestore:"github_repo"                json:"github_repo"`
	WorkspacePath           string        `firestore:"workspace_path"             json:"workspace_path"`
	SlackDefaultChannel     string        `firestore:"slack_default_channel"      json:"slack_default_channel,omitempty"`
	GitHubAppInstallationID int64         `firestore:"github_app_installation_id" json:"github_app_installation_id,omitempty"`
	Status                  ProjectStatus `firestore:"status"                     json:"status"`
	CreatedAt               time.Time     `firestore:"created_at"                 json:"created_at"`
	ArchivedAt              *time.Time    `firestore:"archived_at,omitempty"      json:"archived_at,omitempty"`
}

// ProjectStatus is the lifecycle state of a Project.
type ProjectStatus string

const (
	ProjectStatusActive   ProjectStatus = "active"
	ProjectStatusArchived ProjectStatus = "archived"
)

// project_id rules align with refs/docs/dmail-metadata-v1-1.md.
const projectIDMaxLen = 64

var projectIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateProjectID enforces the project_id format shared by the gateway
// registry and D-Mail metadata v1.1: 1-64 chars, alphanumerics + '-' + '_'.
func ValidateProjectID(id string) error {
	if id == "" {
		return ErrInvalidProjectID
	}
	if len(id) > projectIDMaxLen {
		return ErrInvalidProjectID
	}
	if !projectIDRegex.MatchString(id) {
		return ErrInvalidProjectID
	}
	return nil
}

// Sentinel errors shared by all ProjectRegistry adapters.
var (
	ErrProjectNotFound      = errors.New("project not found")
	ErrProjectAlreadyExists = errors.New("project already exists")
	ErrInvalidProjectID     = errors.New("invalid project id")
)
