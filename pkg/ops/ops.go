// Package ops implements the "ops" command subtree for operational debugging
// of GKE-hosted OpenShift clusters via Cloud Workflows.
//
// This package is self-contained so it can be extracted as a standalone plugin
// binary (gcphcpctl-ops) without depending on the parent pkg/cli package.
package ops

import (
	"fmt"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/ops/companion"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/ops/pam"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/ops/wf"

	"github.com/spf13/cobra"
)

// ValidateRequiredFlags validates that required persistent flags are set.
// Exported so that cmd/ops/main.go can call it from its own PersistentPreRunE.
func ValidateRequiredFlags(cmd *cobra.Command) error {
	project, _ := cmd.Flags().GetString("project")
	if project == "" {
		return fmt.Errorf("--project is required (or set GCPHCPCTL_PROJECT)")
	}
	region, _ := cmd.Flags().GetString("region")
	if region == "" {
		return fmt.Errorf("--region is required (or set GCPHCPCTL_REGION)")
	}
	return nil
}

// NewOpsCmd creates the ops command tree. It can be registered as a subcommand
// of the root gcphcpctl command, or used as the root command of a standalone
// gcphcpctl-ops plugin binary.
func NewOpsCmd() *cobra.Command {
	var opsCmd *cobra.Command
	opsCmd = &cobra.Command{
		Use:   "ops",
		Short: "Operational commands for cluster debugging and remediation",
		Long: `Operational commands for debugging, log analysis, and remediation
of GKE-hosted OpenShift control plane clusters.

Convenience commands (get, logs, describe) run Cloud Workflows under the hood.
Use 'ops wf' for direct workflow management.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if parent := opsCmd.Parent(); parent != nil && parent.PersistentPreRunE != nil {
				if err := parent.PersistentPreRunE(cmd, args); err != nil {
					return err
				}
			}
			return ValidateRequiredFlags(cmd)
		},
	}
	cmd := opsCmd

	cmd.AddCommand(newGetCmd())
	cmd.AddCommand(newLogsCmd())
	cmd.AddCommand(newDescribeCmd())
	cmd.AddCommand(newDiagnoseCmd())
	cmd.AddCommand(newDeleteCmd())
	cmd.AddCommand(newExpandVolumeCmd())
	cmd.AddCommand(newEtcdCmd())
	cmd.AddCommand(newRolloutRestartCmd())
	cmd.AddCommand(wf.NewWfCmd())
	cmd.AddCommand(pam.NewPamCmd())
	cmd.AddCommand(companion.NewCompanionCmd())

	return cmd
}
