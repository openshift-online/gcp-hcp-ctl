package network

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewNetworkCmd returns the parent "network" command with create/destroy subcommands.
func NewNetworkCmd() *cobra.Command {
	var networkCmd *cobra.Command
	networkCmd = &cobra.Command{
		Use:   "network",
		Short: "Manage GCP network infrastructure for HyperShift clusters",
		Long: `Create and destroy GCP network infrastructure including
VPC networks, subnets, Cloud Routers, Cloud NAT, and firewall rules.`,
		// Cobra does not chain PersistentPreRunE from parent commands;
		// manually invoke the parent's hook to ensure config loading runs.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if parent := networkCmd.Parent(); parent != nil && parent.PersistentPreRunE != nil {
				if err := parent.PersistentPreRunE(cmd, args); err != nil {
					return err
				}
			}
			return validateRequiredFlags(cmd)
		},
	}

	networkCmd.AddCommand(NewCreateCommand())
	networkCmd.AddCommand(NewDestroyCommand())

	return networkCmd
}

func validateRequiredFlags(cmd *cobra.Command) error {
	project, err := cmd.Flags().GetString("project")
	if err != nil {
		return fmt.Errorf("failed to read --project flag: %w", err)
	}
	if project == "" {
		return fmt.Errorf("--project is required (or set GCPHCPCTL_PROJECT)")
	}
	region, err := cmd.Flags().GetString("region")
	if err != nil {
		return fmt.Errorf("failed to read --region flag: %w", err)
	}
	if region == "" {
		return fmt.Errorf("--region is required (or set GCPHCPCTL_REGION)")
	}
	return nil
}
