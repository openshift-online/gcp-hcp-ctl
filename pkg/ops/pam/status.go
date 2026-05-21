package pam

import (
	"context"
	"fmt"
	"os"
	"time"

	pamclient "github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/pam"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var entitlement string

	cmd := &cobra.Command{
		Use:   "status <grant-id>",
		Short: "Check the status of a PAM grant",
		Long: `Check the current status of a specific PAM grant.

Examples:
  # Check status by grant ID (entitlement auto-discovered)
  gcphcp ops pam status fd166e71-574a-4420-ba5f-60d5de2e87c9

  # Specify entitlement explicitly
  gcphcp ops pam status fd166e71-574a-4420-ba5f-60d5de2e87c9 --entitlement wf-invoker`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _ := cmd.Flags().GetString("project")
			region, _ := cmd.Flags().GetString("region")
			outputFormat, _ := cmd.Flags().GetString("output")

			if project == "" {
				return fmt.Errorf("--project is required (or set GCPHCP_PROJECT)")
			}
			if region == "" {
				return fmt.Errorf("--region is required (or set GCPHCP_REGION)")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()

			client, err := pamclient.NewClient(ctx, project)
			if err != nil {
				return fmt.Errorf("creating PAM client: %w", err)
			}
			defer client.Close()

			grantName, err := resolveGrantName(ctx, client, project, entitlement, args[0])
			if err != nil {
				return err
			}

			grant, err := client.GetGrant(ctx, grantName)
			if err != nil {
				return fmt.Errorf("getting grant: %w", err)
			}

			return printGrantResult(os.Stdout, outputFormat, grant)
		},
	}

	cmd.Flags().StringVar(&entitlement, "entitlement", "", "Entitlement ID (auto-discovered if not set)")

	return cmd
}
