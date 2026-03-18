package pam

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	pamclient "github.com/ckandag/gcp-hcp-cli/pkg/gcp/pam"
	"github.com/ckandag/gcp-hcp-cli/pkg/output"
	"github.com/spf13/cobra"
)

func newRequestCmd() *cobra.Command {
	var (
		duration time.Duration
		reason   string
		wait     bool
		timeout  time.Duration
	)

	cmd := &cobra.Command{
		Use:   "request [entitlement-id]",
		Short: "Request a PAM grant",
		Long: `Request a new PAM grant for a specific entitlement.

If no entitlement ID is specified, the command discovers available entitlements
and uses the only one found, or errors if multiple exist.

Examples:
  # Request with auto-discovered entitlement
  gcphcp ops pam request --reason "investigating incident INC-123"

  # Request for a specific entitlement
  gcphcp ops pam request wf-invoker --reason "deploying hotfix"

  # Request with custom duration, don't wait for approval
  gcphcp ops pam request --reason "maintenance" --duration 2h --wait=false`,

		Args: cobra.MaximumNArgs(1),
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
			if reason == "" {
				return fmt.Errorf("--reason is required")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			client, err := pamclient.NewClient(ctx, project)
			if err != nil {
				return fmt.Errorf("creating PAM client: %w", err)
			}
			defer client.Close()

			var entitlementName string
			if len(args) > 0 {
				entitlementName = resolveEntitlementName(project, args[0])
			} else {
				entitlementName, err = discoverEntitlement(ctx, client)
				if err != nil {
					return err
				}
			}

			fmt.Fprintf(os.Stderr, "Requesting PAM grant for entitlement: %s\n", pamclient.ShortEntitlementName(entitlementName))
			fmt.Fprintf(os.Stderr, "Duration: %s  Reason: %s\n", duration, reason)

			grant, err := client.CreateGrant(ctx, entitlementName, duration, reason)
			if err != nil {
				return fmt.Errorf("requesting grant: %w", err)
			}

			fmt.Fprintf(os.Stderr, "Grant created: %s (state: %s)\n", grant.ShortName(), grant.State)

			if !wait || grant.State != "APPROVAL_AWAITED" {
				return printGrantResult(os.Stdout, outputFormat, grant)
			}

			fmt.Fprintf(os.Stderr, "Waiting for approval... (Ctrl+C to cancel)\n")
			fmt.Fprintf(os.Stderr, "  Check status: gcphcp ops pam status %s\n", grant.Name)

			grant, err = client.WaitForGrant(ctx, grant.Name)
			if err != nil {
				return err
			}

			switch grant.State {
			case "ACTIVE", "ACTIVATED":
				fmt.Fprintf(os.Stderr, "Grant approved and active!\n")
			case "DENIED":
				fmt.Fprintf(os.Stderr, "Grant was denied.\n")
			case "EXPIRED":
				fmt.Fprintf(os.Stderr, "Grant expired before approval.\n")
			default:
				fmt.Fprintf(os.Stderr, "Grant state: %s\n", grant.State)
			}

			return printGrantResult(os.Stdout, outputFormat, grant)
		},
	}

	cmd.Flags().DurationVar(&duration, "duration", 1*time.Hour, "Requested grant duration")
	cmd.Flags().StringVar(&reason, "reason", "", "Justification for the grant request (required)")
	cmd.Flags().BoolVar(&wait, "wait", true, "Wait for grant approval")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "Maximum time to wait for approval")

	return cmd
}

// resolveGrantName builds the full grant resource name from a grant ID.
// If the grantID already contains "/", it's treated as a full resource name.
// Otherwise, the entitlement is resolved (explicit or auto-discovered) and the path is built.
func resolveGrantName(ctx context.Context, client *pamclient.Client, project, entitlement, grantID string) (string, error) {
	if strings.Contains(grantID, "/") {
		return grantID, nil
	}

	var entitlementName string
	if entitlement != "" {
		entitlementName = resolveEntitlementName(project, entitlement)
	} else {
		var err error
		entitlementName, err = discoverEntitlement(ctx, client)
		if err != nil {
			return "", err
		}
	}

	return entitlementName + "/grants/" + grantID, nil
}

func resolveEntitlementName(project, entID string) string {
	if strings.Contains(entID, "/") {
		return entID
	}
	// PAM entitlements are global resources, not regional
	return fmt.Sprintf("projects/%s/locations/global/entitlements/%s", project, entID)
}

func discoverEntitlement(ctx context.Context, client *pamclient.Client) (string, error) {
	entitlements, err := client.SearchEntitlements(ctx)
	if err != nil {
		return "", fmt.Errorf("searching entitlements: %w", err)
	}
	if len(entitlements) == 0 {
		return "", fmt.Errorf("no PAM entitlements found for your account\n\n" +
			"  Ensure you are an eligible requester for a PAM entitlement in this project/region.\n" +
			"  Check with your administrator.")
	}
	if len(entitlements) > 1 {
		fmt.Fprintf(os.Stderr, "Multiple entitlements available:\n")
		for _, e := range entitlements {
			fmt.Fprintf(os.Stderr, "  - %s (max: %s)\n", pamclient.ShortEntitlementName(e.Name), e.MaxDuration)
		}
		return "", fmt.Errorf("multiple entitlements found; specify one as an argument")
	}
	return entitlements[0].Name, nil
}

func printGrantResult(w io.Writer, outputFormat string, grant *pamclient.GrantInfo) error {
	format := output.ParseFormat(outputFormat)
	if format == output.FormatJSON {
		return output.PrintJSON(w, grant)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "GRANT STATUS")
	fmt.Fprintln(w, "============")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  Grant:       %s\n", grant.ShortName())
	fmt.Fprintf(w, "  State:       %s\n", grant.State)
	fmt.Fprintf(w, "  Requester:   %s\n", grant.Requester)
	fmt.Fprintf(w, "  Duration:    %s\n", grant.RequestedDuration)
	fmt.Fprintf(w, "  Entitlement: %s\n", grant.ShortEntitlement())
	fmt.Fprintf(w, "  Created:     %s\n", grant.CreateTime.Format(time.RFC3339))
	if !grant.ActivateTime.IsZero() {
		fmt.Fprintf(w, "  Activated:   %s\n", grant.ActivateTime.Format(time.RFC3339))
	}
	if remaining := grant.RemainingTime(); remaining > 0 {
		fmt.Fprintf(w, "  Remaining:   %s\n", remaining)
	}
	fmt.Fprintln(w)

	return nil
}
