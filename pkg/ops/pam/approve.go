package pam

import (
	"context"
	"fmt"
	"os"
	"time"

	pamclient "github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/pam"
	"github.com/spf13/cobra"
)

func newApproveCmd() *cobra.Command {
	var (
		reason      string
		entitlement string
	)

	cmd := &cobra.Command{
		Use:   "approve <grant-id>",
		Short: "Approve a pending PAM grant",
		Long: `Approve a pending PAM grant request (for approvers).

Examples:
  # Approve by grant ID (entitlement auto-discovered)
  gcphcp ops pam approve fd166e71-574a-4420-ba5f-60d5de2e87c9

  # Approve with a reason
  gcphcp ops pam approve fd166e71-574a-4420-ba5f-60d5de2e87c9 --reason "approved for maintenance window"

  # Specify entitlement explicitly
  gcphcp ops pam approve fd166e71-574a-4420-ba5f-60d5de2e87c9 --entitlement wf-invoker`,

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

			fmt.Fprintf(os.Stderr, "Approving grant...\n")

			grant, err := client.ApproveGrant(ctx, grantName, reason)
			if err != nil {
				return fmt.Errorf("approving grant: %w", err)
			}

			fmt.Fprintf(os.Stderr, "Grant approved: %s (state: %s)\n", grant.ShortName(), grant.State)

			return printGrantResult(os.Stdout, outputFormat, grant)
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "Reason for approval")
	cmd.Flags().StringVar(&entitlement, "entitlement", "", "Entitlement ID (auto-discovered if not set)")

	return cmd
}
