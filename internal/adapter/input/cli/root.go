// Package cli implements the CLI adapter for runops-gateway using Cobra.
// It provides the "approve", "deny" and "project" sub-commands that drive
// the RunOpsUseCase and ProjectRegistry. Slack is NOT required: passing
// --no-slack (or using Source="cli") enables fully standalone operation as
// described in ADR 0007.
package cli

import (
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/spf13/cobra"
)

// NewRootCmd creates the root runops CLI command. registry may be nil; when
// nil, the `project` sub-command tree is not registered (handy for
// composition roots that wire only the approval use case).
func NewRootCmd(useCase port.RunOpsUseCase, registry port.ProjectRegistry) *cobra.Command {
	root := &cobra.Command{
		Use:   "runops",
		Short: "runops-gateway CLI — operate GCP resources via ChatOps",
	}
	root.AddCommand(newApproveCmd(useCase))
	root.AddCommand(newDenyCmd(useCase))
	if registry != nil {
		root.AddCommand(newProjectCmd(registry))
	}
	return root
}
