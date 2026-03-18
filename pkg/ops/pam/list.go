package pam

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	pamclient "github.com/ckandag/gcp-hcp-cli/pkg/gcp/pam"
	"github.com/ckandag/gcp-hcp-cli/pkg/output"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var (
		entitlement string
		mine        bool
		approvals   bool
		state       string
		timeout     time.Duration
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List PAM grants",
		Long: `List PAM grants visible to the current user.

By default, shows active and pending grants. Use --state all to include
historical grants (ended, expired, denied, withdrawn).

Use --mine to show only grants you created, or --approvals to see
grants pending your approval (for approvers).

Examples:
  # List active and pending grants (default)
  gcphcp ops pam list

  # Include all historical grants
  gcphcp ops pam list --state all

  # List only active grants
  gcphcp ops pam list --state active

  # List only your grants
  gcphcp ops pam list --mine

  # List grants pending your approval
  gcphcp ops pam list --approvals

  # List grants for a specific entitlement
  gcphcp ops pam list --entitlement wf-invoker`,

		Args: cobra.NoArgs,
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

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			client, err := pamclient.NewClient(ctx, project)
			if err != nil {
				return fmt.Errorf("creating PAM client: %w", err)
			}
			defer client.Close()

			// Resolve entitlement names to search
			var entitlementNames []string
			if entitlement != "" {
				entitlementNames = []string{resolveEntitlementName(project, entitlement)}
			} else {
				ents, err := client.SearchEntitlements(ctx)
				if err != nil {
					return fmt.Errorf("searching entitlements: %w", err)
				}
				if len(ents) == 0 {
					fmt.Fprintln(os.Stderr, "No PAM entitlements found for your account.")
					return nil
				}
				for _, e := range ents {
					entitlementNames = append(entitlementNames, e.Name)
				}
			}

			var grants []pamclient.GrantInfo
			for _, entName := range entitlementNames {
				if mine {
					found, err := client.SearchGrants(ctx, entName, pamclient.RelationshipCreated)
					if err != nil {
						return fmt.Errorf("searching grants: %w", err)
					}
					grants = append(grants, found...)
				} else if approvals {
					found, err := client.SearchGrants(ctx, entName, pamclient.RelationshipCanApprove)
					if err != nil {
						return fmt.Errorf("searching grants: %w", err)
					}
					grants = append(grants, found...)
				} else {
					// Default: list all grants for this entitlement
					found, err := client.ListGrants(ctx, entName, "")
					if err != nil {
						return fmt.Errorf("listing grants: %w", err)
					}
					grants = append(grants, found...)
				}
			}

			// Filter by state
			if !strings.EqualFold(state, "all") && state != "" {
				allowed := make(map[string]bool)
				for _, s := range strings.Split(state, ",") {
					allowed[strings.ToUpper(strings.TrimSpace(s))] = true
				}
				var filtered []pamclient.GrantInfo
				for _, g := range grants {
					if allowed[g.State] {
						filtered = append(filtered, g)
					}
				}
				grants = filtered
			}

			format := output.ParseFormat(outputFormat)
			if format == output.FormatJSON {
				return output.PrintJSON(os.Stdout, grants)
			}

			if len(grants) == 0 {
				fmt.Fprintln(os.Stdout, "No grants found.")
				return nil
			}

			t := output.NewTable(os.Stdout, "ID", "ENTITLEMENT", "STATE", "REQUESTER", "CREATED", "DURATION", "REMAINING")
			for _, g := range grants {
				created := output.Age(g.CreateTime.Format(time.RFC3339))
				remaining := ""
				if r := g.RemainingTime(); r > 0 {
					remaining = r.String()
				}
				t.AddRow(
					g.ShortName(),
					g.ShortEntitlement(),
					g.State,
					g.Requester,
					created,
					g.RequestedDuration.String(),
					remaining,
				)
			}
			return t.Flush()
		},
	}

	cmd.Flags().StringVar(&entitlement, "entitlement", "", "Filter by entitlement ID")
	cmd.Flags().BoolVar(&mine, "mine", false, "List only grants you created")
	cmd.Flags().BoolVar(&approvals, "approvals", false, "List grants pending your approval (for approvers)")
	cmd.Flags().StringVar(&state, "state", "ACTIVE,APPROVAL_AWAITED", "Filter by grant state, comma-separated: ACTIVE, APPROVAL_AWAITED, DENIED, ENDED, EXPIRED, REVOKED, WITHDRAWN (use \"all\" for no filter)")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Maximum time for API calls")

	return cmd
}
