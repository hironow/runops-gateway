package domain

import (
	"errors"
	"regexp"
	"time"
)

// Project is a multiplex registry entry. Shared by SQLite (dev/test) and
// Firestore (production, issue #0011) adapters via port.ProjectRegistry.
type Project struct {
	ID                      string
	GitHubOrg               string
	GitHubRepo              string
	WorkspacePath           string
	SlackDefaultChannel     string // "" = unset
	GitHubAppInstallationID int64  // 0 = unset; validated in issue #0010
	Status                  ProjectStatus
	CreatedAt               time.Time
	ArchivedAt              *time.Time // non-nil only when Status == ProjectStatusArchived
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
