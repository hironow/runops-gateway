// Package cli implements the CLI adapter for runops-gateway using Cobra.
// It provides the "approve" and "deny" sub-commands that drive the RunOpsUseCase.
// Slack is NOT required: passing --no-slack (or using Source="cli") enables
// fully standalone operation as described in ADR 0007.
package cli

import (
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/spf13/cobra"
)

// NewRootCmd creates the root runops CLI command.
func NewRootCmd(useCase port.RunOpsUseCase) *cobra.Command {
	root := &cobra.Command{
		Use:   "runops",
		Short: "runops-gateway CLI — operate GCP resources via ChatOps",
	}
	root.AddCommand(newApproveCmd(useCase))
	root.AddCommand(newDenyCmd(useCase))
	return root
}
