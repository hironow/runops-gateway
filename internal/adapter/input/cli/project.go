package cli

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/spf13/cobra"
)

// newProjectCmd builds the `runops project ...` subcommand tree backed by a
// ProjectRegistry. It returns nil when registry is nil (no DB wired in).
//
// The CLI is intended for operator local Mac development only. Production
// registry mutation flows through gateway HTTP admin endpoints (issue
// #0012); see ADR 0026.
func newProjectCmd(registry port.ProjectRegistry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage the multiplex project registry (dev/local only)",
	}
	cmd.AddCommand(
		newProjectAddCmd(registry),
		newProjectListCmd(registry),
		newProjectShowCmd(registry),
		newProjectArchiveCmd(registry),
	)
	return cmd
}

func newProjectAddCmd(registry port.ProjectRegistry) *cobra.Command {
	var org, repo, workspace, slackChannel string
	var installationID int64
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Add a project to the registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := domain.Project{
				ID:                      args[0],
				GitHubOrg:               org,
				GitHubRepo:              repo,
				WorkspacePath:           workspace,
				SlackDefaultChannel:     slackChannel,
				GitHubAppInstallationID: installationID,
			}
			if err := registry.Add(context.Background(), p); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added project %s\n", p.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "GitHub organization (required)")
	cmd.Flags().StringVar(&repo, "repo", "", "GitHub repository (required)")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Absolute workspace path (required)")
	cmd.Flags().StringVar(&slackChannel, "slack-channel", "", "Slack default channel (e.g. #runops)")
	cmd.Flags().Int64Var(&installationID, "installation-id", 0, "GitHub App installation ID")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("repo")
	_ = cmd.MarkFlagRequired("workspace")
	return cmd
}

func newProjectListCmd(registry port.ProjectRegistry) *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List projects in the registry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			filter, err := buildListFilter(status)
			if err != nil {
				return err
			}
			projects, err := registry.List(context.Background(), filter)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tORG\tREPO\tWORKSPACE\tSTATUS")
			for _, p := range projects {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					p.ID, p.GitHubOrg, p.GitHubRepo, p.WorkspacePath, p.Status)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "Filter by status: active, archived (default: all)")
	return cmd
}

func buildListFilter(status string) (port.ProjectListFilter, error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "all":
		return port.ProjectListFilter{}, nil
	case "active":
		return port.ProjectListFilter{Status: domain.ProjectStatusActive}, nil
	case "archived":
		return port.ProjectListFilter{Status: domain.ProjectStatusArchived}, nil
	default:
		return port.ProjectListFilter{}, fmt.Errorf("unknown --status: %q (want active|archived|all)", status)
	}
}

func newProjectShowCmd(registry port.ProjectRegistry) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show a single project's fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := registry.Get(context.Background(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "id:                       %s\n", p.ID)
			fmt.Fprintf(out, "github_org:               %s\n", p.GitHubOrg)
			fmt.Fprintf(out, "github_repo:              %s\n", p.GitHubRepo)
			fmt.Fprintf(out, "workspace_path:           %s\n", p.WorkspacePath)
			fmt.Fprintf(out, "slack_default_channel:    %s\n", p.SlackDefaultChannel)
			fmt.Fprintf(out, "github_app_installation_id: %d\n", p.GitHubAppInstallationID)
			fmt.Fprintf(out, "status:                   %s\n", p.Status)
			fmt.Fprintf(out, "created_at:               %s\n", p.CreatedAt.Format("2006-01-02 15:04:05"))
			if p.ArchivedAt != nil {
				fmt.Fprintf(out, "archived_at:              %s\n", p.ArchivedAt.Format("2006-01-02 15:04:05"))
			}
			return nil
		},
	}
}

func newProjectArchiveCmd(registry port.ProjectRegistry) *cobra.Command {
	return &cobra.Command{
		Use:   "archive <id>",
		Short: "Mark a project as archived (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := registry.Archive(context.Background(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "archived project %s\n", args[0])
			return nil
		},
	}
}

