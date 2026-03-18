// Package wf implements the "ops wf" command subtree for direct
// Cloud Workflow management (run, list, status, resume).
package wf

import (
	"github.com/spf13/cobra"
)

// NewWfCmd creates the wf command tree.
func NewWfCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wf",
		Short: "Manage Cloud Workflows directly",
		Long: `Direct Cloud Workflow management commands.

Use these for running arbitrary workflows, checking execution status,
listing workflows and execution history, and resuming paused workflows.`,
	}

	cmd.AddCommand(newRunCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newResumeCmd())
	cmd.AddCommand(newAuditCmd())

	return cmd
}
