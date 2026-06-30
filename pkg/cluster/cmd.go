package cluster

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/auth"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/hyperfleet"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	"github.com/spf13/cobra"
)

type contextKey string

const clientKey contextKey = "hyperfleet-client"

// NewClusterCmd returns the "cluster" command group.
func NewClusterCmd() *cobra.Command {
	var clusterCmd *cobra.Command
	clusterCmd = &cobra.Command{
		Use:   "cluster",
		Short: "Manage HyperFleet clusters",
		Long:  `Create, get, list, delete, and log in to HyperFleet clusters via the HyperFleet API.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if parent := clusterCmd.Parent(); parent != nil && parent.PersistentPreRunE != nil {
				if err := parent.PersistentPreRunE(cmd, args); err != nil {
					return err
				}
			}
			if err := validateRequiredFlags(cmd); err != nil {
				return err
			}
			apiEndpoint, _ := cmd.Flags().GetString("api-endpoint")
			client, err := newClient(apiEndpoint)
			if err != nil {
				return err
			}
			cmd.SetContext(context.WithValue(cmd.Context(), clientKey, client))
			return nil
		},
	}

	clusterCmd.AddCommand(newCreateCmd())
	clusterCmd.AddCommand(newGetCmd())
	clusterCmd.AddCommand(newListCmd())
	clusterCmd.AddCommand(newDeleteCmd())
	clusterCmd.AddCommand(newLoginCmd())

	return clusterCmd
}

func validateRequiredFlags(cmd *cobra.Command) error {
	apiEndpoint, _ := cmd.Flags().GetString("api-endpoint")
	if apiEndpoint == "" {
		return fmt.Errorf("--api-endpoint is required (or set GCPHCPCTL_API_ENDPOINT or api_endpoint in config)")
	}
	return nil
}

func newClient(apiEndpoint string) (*hyperfleet.ClientWithResponses, error) {
	return newClientWithTokenSource(apiEndpoint, auth.NewTokenSource())
}

func newClientWithTokenSource(apiEndpoint string, ts *auth.TokenSource) (*hyperfleet.ClientWithResponses, error) {
	if apiEndpoint == "" {
		return nil, fmt.Errorf("--api-endpoint is required (or set GCPHCPCTL_API_ENDPOINT or api_endpoint in config)")
	}
	return hyperfleet.NewAPIClient(apiEndpoint, ts)
}

func clientFromCmd(cmd *cobra.Command) *hyperfleet.ClientWithResponses {
	return cmd.Context().Value(clientKey).(*hyperfleet.ClientWithResponses)
}

// resolveCluster looks up a cluster by name or ID. It first tries a
// direct ID lookup, and if that returns 404, falls back to searching
// clusters by name using the API search filter.
func resolveCluster(ctx context.Context, client *hyperfleet.ClientWithResponses, ref string) (*hyperfleet.Cluster, error) {
	resp, err := client.GetClusterByIdWithResponse(ctx, ref, nil)
	if err != nil {
		return nil, fmt.Errorf("looking up cluster %q: %w", ref, err)
	}
	if resp.JSON200 != nil {
		return resp.JSON200, nil
	}
	if resp.HTTPResponse == nil || resp.HTTPResponse.StatusCode != http.StatusNotFound {
		return nil, fmt.Errorf("looking up cluster %q: %s", ref, formatError(resp.HTTPResponse, resp.Body))
	}

	escapedRef := strings.ReplaceAll(ref, "'", "\\'")
	search := fmt.Sprintf("name = '%s'", escapedRef)
	var page int32 = 1
	var pageSize int32 = 100
	for {
		params := &hyperfleet.GetClustersParams{
			Search:   &search,
			Page:     &page,
			PageSize: &pageSize,
		}
		listResp, err := client.GetClustersWithResponse(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("listing clusters: %w", err)
		}
		if listResp.JSON200 == nil {
			return nil, fmt.Errorf("listing clusters: %s", formatError(listResp.HTTPResponse, listResp.Body))
		}
		for i := range listResp.JSON200.Items {
			c := &listResp.JSON200.Items[i]
			if c.Name == ref {
				return c, nil
			}
		}
		if len(listResp.JSON200.Items) == 0 || page*pageSize >= listResp.JSON200.Total {
			break
		}
		page++
	}
	return nil, fmt.Errorf("cluster %q not found", ref)
}

func formatError(resp *http.Response, body []byte) string {
	msg := string(body)
	if len(msg) > 500 {
		msg = msg[:500] + "..."
	}
	if resp == nil {
		if msg == "" {
			return "HTTP response unavailable"
		}
		return fmt.Sprintf("HTTP response unavailable: %s", msg)
	}
	return fmt.Sprintf("HTTP %d: %s", resp.StatusCode, msg)
}

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func printCluster(w io.Writer, c *hyperfleet.Cluster, format string) error {
	switch output.ParseFormat(format) {
	case output.FormatJSON:
		return output.PrintJSON(w, c)
	case output.FormatYAML:
		return output.PrintYAML(w, c)
	default:
	}

	bw := bufio.NewWriter(w)

	fmt.Fprintf(bw, "Name:       %s\n", c.Name)
	fmt.Fprintf(bw, "ID:         %s\n", ptrStr(c.Id))
	fmt.Fprintf(bw, "Generation: %d\n", c.Generation)
	fmt.Fprintf(bw, "Region:     %s\n", c.Spec.Platform.Gcp.Region)
	fmt.Fprintf(bw, "Project:    %s\n", c.Spec.Platform.Gcp.ProjectID)
	if c.Spec.Release != nil && c.Spec.Release.Version != nil {
		fmt.Fprintf(bw, "Version:    %s\n", *c.Spec.Release.Version)
	}
	fmt.Fprintf(bw, "Status:     %s\n", clusterStatusDetail(c))
	if !c.CreatedTime.IsZero() {
		fmt.Fprintf(bw, "CreatedAt:  %s\n", c.CreatedTime.Format("2006-01-02T15:04:05Z"))
	}
	if c.CreatedBy != "" {
		fmt.Fprintf(bw, "CreatedBy:  %s\n", string(c.CreatedBy))
	}
	if c.DeletedTime != nil {
		fmt.Fprintf(bw, "DeletedAt:  %s\n", c.DeletedTime.Format("2006-01-02T15:04:05Z"))
	}
	if c.DeletedBy != nil {
		fmt.Fprintf(bw, "DeletedBy:  %s\n", string(*c.DeletedBy))
	}

	if len(c.Status.Conditions) > 0 {
		fmt.Fprintln(bw, "\nConditions:")
		t := output.NewTable(bw, "TYPE", "STATUS", "GEN", "REASON", "MESSAGE")
		for _, cond := range c.Status.Conditions {
			msg := ptrStr(cond.Message)
			if len(msg) > 80 {
				msg = msg[:80] + "..."
			}
			t.AddRow(cond.Type, string(cond.Status), fmt.Sprintf("%d", cond.ObservedGeneration), ptrStr(cond.Reason), msg)
		}
		if err := t.Flush(); err != nil {
			return err
		}
	}

	return bw.Flush()
}

func findCondition(conditions []hyperfleet.ResourceCondition, condType string) *hyperfleet.ResourceCondition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

// clusterStatus returns a short human-friendly phase for table/list output.
func clusterStatus(c *hyperfleet.Cluster) string {
	phase, _ := deriveClusterStatus(c)
	return phase
}

// clusterStatusDetail returns a phase with parenthetical explanation for get output.
func clusterStatusDetail(c *hyperfleet.Cluster) string {
	phase, detail := deriveClusterStatus(c)
	if detail == "" {
		return phase
	}
	return fmt.Sprintf("%s (%s)", phase, detail)
}

func deriveClusterStatus(c *hyperfleet.Cluster) (phase, detail string) {
	if c.DeletedTime != nil {
		return "Deleting", ""
	}

	conditions := c.Status.Conditions
	if len(conditions) == 0 {
		return "Pending", ""
	}

	reconciled := findCondition(conditions, "Reconciled")
	lastKnown := findCondition(conditions, "LastKnownReconciled")

	if reconciled != nil && reconciled.Status == hyperfleet.ResourceConditionStatusTrue {
		return "Ready", ""
	}

	if reconciled != nil && reconciled.Status == hyperfleet.ResourceConditionStatusFalse {
		if lastKnown != nil && lastKnown.Status == hyperfleet.ResourceConditionStatusTrue {
			return "Degraded", conditionSummary(reconciled, c.Generation)
		}

		return "Progressing", ""
	}

	return "Progressing", ""
}

func conditionSummary(cond *hyperfleet.ResourceCondition, generation int32) string {
	reason := ptrStr(cond.Reason)
	msg := ptrStr(cond.Message)

	if cond.ObservedGeneration < generation && cond.ObservedGeneration > 0 {
		return fmt.Sprintf("adapters finalizing generation %d", generation)
	}

	if msg != "" {
		if len(msg) > 60 {
			msg = msg[:60] + "..."
		}
		return msg
	}
	return reason
}


func releaseVersion(c *hyperfleet.Cluster) string {
	if c.Spec.Release != nil && c.Spec.Release.Version != nil && *c.Spec.Release.Version != "" {
		return *c.Spec.Release.Version
	}
	return "<none>"
}

func truncateID(id string) string {
	if len(id) > 12 {
		return id[:12] + "..."
	}
	return id
}
