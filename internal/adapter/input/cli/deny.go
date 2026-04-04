package cli

import (
	"context"
	"fmt"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/spf13/cobra"
)

func newDenyCmd(useCase port.RunOpsUseCase) *cobra.Command {
	var project, location, approver string
	var noSlack bool

	cmd := &cobra.Command{
		Use:   "deny <resource-type> <resource-name>",
		Short: "Deny a pending ChatOps operation",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if approver == "" {
				approver = gitUserEmail()
			}
			req := domain.ApprovalRequest{
				Project:       project,
				Location:      location,
				ResourceType:  domain.ResourceType(args[0]),
				ResourceNames: args[1],
				ApproverID:    approver,
				IssuedAt:      0,
			}
			notify := port.NotifyTarget{Mode: port.ModeStdout}
			if err := useCase.DenyAction(context.Background(), req, notify); err != nil {
				return fmt.Errorf("deny failed: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Operation denied.")
			return nil
		},
	}

	cmd.Flags().StringVar(&project, "project", "", "GCP project ID of the target resource (required)")
	cmd.Flags().StringVar(&location, "location", "", "GCP region of the target resource (required)")
	cmd.Flags().StringVar(&approver, "approver", "", "Approver ID or email")
	cmd.Flags().BoolVar(&noSlack, "no-slack", false, "Disable Slack notifications")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("location")
	return cmd
}
