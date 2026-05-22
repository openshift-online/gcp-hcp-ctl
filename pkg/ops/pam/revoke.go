package pam

import (
	"context"
	"fmt"
	"os"
	"time"

	pamclient "github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/pam"
	"github.com/spf13/cobra"
)

func newRevokeCmd() *cobra.Command {
	var (
		reason      string
		entitlement string
	)

	cmd := &cobra.Command{
		Use:   "revoke <grant-id>",
		Short: "Revoke an active PAM grant",
		Long: `Revoke an active PAM grant to immediately remove elevated access.

Use this to release access you no longer need instead of waiting for expiry.

Examples:
  # Revoke a grant
  gcphcpctl ops pam revoke fd166e71-574a-4420-ba5f-60d5de2e87c9 --reason "work completed"

  # Specify entitlement explicitly
  gcphcpctl ops pam revoke fd166e71-574a-4420-ba5f-60d5de2e87c9 --entitlement wf-invoker`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _ := cmd.Flags().GetString("project")
			outputFormat, _ := cmd.Flags().GetString("output")


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

			fmt.Fprintf(os.Stderr, "Revoking grant...\n")

			grant, err := client.RevokeGrant(ctx, grantName, reason)
			if err != nil {
				return fmt.Errorf("revoking grant: %w", err)
			}

			fmt.Fprintf(os.Stderr, "Grant revoked: %s (state: %s)\n", grant.ShortName(), grant.State)

			return printGrantResult(os.Stdout, outputFormat, grant)
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "Reason for revocation")
	cmd.Flags().StringVar(&entitlement, "entitlement", "", "Entitlement ID (auto-discovered if not set)")

	return cmd
}
