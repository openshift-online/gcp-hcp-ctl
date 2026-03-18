// Package pam implements the "ops pam" command subtree for managing
// GCP Privileged Access Manager grants.
package pam

import (
	"github.com/spf13/cobra"
)

// NewPamCmd creates the pam command tree.
func NewPamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pam",
		Short: "Manage PAM (Privileged Access Manager) grants",
		Long: `Manage Privileged Access Manager grants for PAM-gated workflows.

PAM controls temporary elevated access to execute protected Cloud Workflows.
Use these commands to request, approve, and monitor grants.

Examples:
  # List your grants
  gcphcp ops pam list

  # Request a new grant
  gcphcp ops pam request --reason "investigating incident INC-123"

  # Check grant status
  gcphcp ops pam status <grant-id>

  # Approve a pending grant (for approvers)
  gcphcp ops pam approve <grant-id>

  # Deny a pending grant (for approvers)
  gcphcp ops pam deny <grant-id>

  # Revoke an active grant
  gcphcp ops pam revoke <grant-id> --reason "work completed"`,
	}

	cmd.AddCommand(newRequestCmd())
	cmd.AddCommand(newApproveCmd())
	cmd.AddCommand(newDenyCmd())
	cmd.AddCommand(newRevokeCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newListCmd())

	return cmd
}
