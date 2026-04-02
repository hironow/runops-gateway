package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/spf13/cobra"
)

func newApproveCmd(useCase port.RunOpsUseCase) *cobra.Command {
	var action, target, approver string
	var noSlack bool

	cmd := &cobra.Command{
		Use:   "approve <resource-type> <resource-name>",
		Short: "Approve and execute a pending ChatOps operation",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resourceType := args[0]
			resourceName := args[1]

			if action == "" {
				return fmt.Errorf("--action is required")
			}
			if approver == "" {
				approver = gitUserEmail()
			}

			req := domain.ApprovalRequest{
				ResourceType: domain.ResourceType(resourceType),
				ResourceName: resourceName,
				Target:       target,
				Action:       action,
				ApproverID:   approver,
				Source:       "cli",
				IssuedAt:     0, // CLI mode: no expiry
				ResponseURL:  "", // CLI mode: no Slack response URL
			}

			if err := useCase.ApproveAction(context.Background(), req); err != nil {
				return fmt.Errorf("approval failed: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Successfully approved and executed.")
			return nil
		},
	}

	cmd.Flags().StringVar(&action, "action", "", "Action to perform (e.g. canary_10, migrate_apply)")
	cmd.Flags().StringVar(&target, "target", "", "Revision name (for Cloud Run Service)")
	cmd.Flags().StringVar(&approver, "approver", "", "Approver ID or email (defaults to git config user.email)")
	cmd.Flags().BoolVar(&noSlack, "no-slack", false, "Disable Slack notifications (required when Slack is down)")
	// noSlack flag is passed via Source="cli" which triggers StdoutNotifier in wiring

	return cmd
}

// gitUserEmail returns the git config user.email or a fallback.
func gitUserEmail() string {
	out, err := exec.Command("git", "config", "user.email").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "unknown@cli"
	}
	return strings.TrimSpace(string(out))
}
