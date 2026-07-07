package nodepool

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

// defaultShardLabel is required by the nodepool sentinel's resource_selector
// for discovery. Without it, sentinels filtering on shard="1" will not pick
// up the nodepool and adapters will never reconcile it.
const defaultShardLabel = "1"

// NewNodePoolCmd returns the "nodepool" command group.
func NewNodePoolCmd() *cobra.Command {
	var npCmd *cobra.Command
	npCmd = &cobra.Command{
		Use:          "nodepool",
		Short:        "Manage HyperFleet nodepools",
		Long:         `Create, get, list, delete, and scale nodepools via the HyperFleet API.`,
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if parent := npCmd.Parent(); parent != nil && parent.PersistentPreRunE != nil {
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

	npCmd.AddCommand(newCreateCmd())
	npCmd.AddCommand(newGetCmd())
	npCmd.AddCommand(newListCmd())
	npCmd.AddCommand(newDeleteCmd())
	npCmd.AddCommand(newScaleCmd())

	return npCmd
}

func validateRequiredFlags(cmd *cobra.Command) error {
	apiEndpoint, _ := cmd.Flags().GetString("api-endpoint")
	if apiEndpoint == "" {
		return fmt.Errorf("--api-endpoint is required (or set GCPHCPCTL_API_ENDPOINT or api_endpoint in config)")
	}
	return nil
}

func newClient(apiEndpoint string) (*hyperfleet.ClientWithResponses, error) {
	if apiEndpoint == "" {
		return nil, fmt.Errorf("--api-endpoint is required (or set GCPHCPCTL_API_ENDPOINT or api_endpoint in config)")
	}
	return hyperfleet.NewAPIClient(apiEndpoint, auth.NewTokenSource())
}

func clientFromCmd(cmd *cobra.Command) *hyperfleet.ClientWithResponses {
	client, ok := cmd.Context().Value(clientKey).(*hyperfleet.ClientWithResponses)
	if !ok {
		panic("bug: clientFromCmd called before PersistentPreRunE set the HyperFleet client")
	}
	return client
}

// resolveCluster looks up a cluster by name or ID, reusing the same
// pattern as the cluster package.
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

// resolveNodePool looks up a nodepool by name or ID. It uses the
// Search TSL filter to narrow results server-side, then scans
// page-by-page to avoid fetching the entire nodepool list.
func resolveNodePool(ctx context.Context, client *hyperfleet.ClientWithResponses, ref string) (*hyperfleet.NodePool, string, error) {
	escapedRef := strings.ReplaceAll(ref, "'", "\\'")
	search := fmt.Sprintf("name = '%s' or id = '%s'", escapedRef, escapedRef)
	var page int32 = 1
	var pageSize int32 = 100

	for {
		params := &hyperfleet.GetNodePoolsParams{
			Search:   &search,
			Page:     &page,
			PageSize: &pageSize,
		}
		resp, err := client.GetNodePoolsWithResponse(ctx, params)
		if err != nil {
			return nil, "", fmt.Errorf("listing nodepools: %w", err)
		}
		if resp.JSON200 == nil {
			return nil, "", fmt.Errorf("listing nodepools: %s", formatError(resp.HTTPResponse, resp.Body))
		}

		for i := range resp.JSON200.Items {
			np := &resp.JSON200.Items[i]
			if ptrStr(np.Id) == ref || np.Name == ref {
				clusterID := ptrStr(np.OwnerReferences.Id)
				if clusterID == "" {
					return nil, "", fmt.Errorf("nodepool %q has no owner cluster reference", ref)
				}
				return np, clusterID, nil
			}
		}

		if int32(len(resp.JSON200.Items)) < pageSize {
			break
		}
		page++
	}
	return nil, "", fmt.Errorf("nodepool %q not found", ref)
}

// fetchNodePools retrieves all nodepools matching the given parameters,
// handling pagination automatically.
func fetchNodePools(ctx context.Context, client *hyperfleet.ClientWithResponses, clusterID string) ([]hyperfleet.NodePool, error) {
	var all []hyperfleet.NodePool
	var page int32 = 1
	var pageSize int32 = 100

	for {
		if clusterID != "" {
			params := &hyperfleet.GetNodePoolsByClusterIdParams{
				Page:     &page,
				PageSize: &pageSize,
			}
			resp, err := client.GetNodePoolsByClusterIdWithResponse(ctx, clusterID, params)
			if err != nil {
				return nil, fmt.Errorf("listing nodepools: %w", err)
			}
			if resp.JSON200 == nil {
				return nil, fmt.Errorf("listing nodepools: %s", formatError(resp.HTTPResponse, resp.Body))
			}
			all = append(all, resp.JSON200.Items...)
			if len(resp.JSON200.Items) == 0 || int32(len(resp.JSON200.Items)) < pageSize || int32(len(all)) >= resp.JSON200.Total {
				break
			}
		} else {
			params := &hyperfleet.GetNodePoolsParams{
				Page:     &page,
				PageSize: &pageSize,
			}
			resp, err := client.GetNodePoolsWithResponse(ctx, params)
			if err != nil {
				return nil, fmt.Errorf("listing nodepools: %w", err)
			}
			if resp.JSON200 == nil {
				return nil, fmt.Errorf("listing nodepools: %s", formatError(resp.HTTPResponse, resp.Body))
			}
			all = append(all, resp.JSON200.Items...)
			if len(resp.JSON200.Items) == 0 || int32(len(resp.JSON200.Items)) < pageSize || int32(len(all)) >= resp.JSON200.Total {
				break
			}
		}
		page++
	}
	return all, nil
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

func intPtr(i int) *int {
	return &i
}

func printNodePool(w io.Writer, np *hyperfleet.NodePool, format string) error {
	switch output.ParseFormat(format) {
	case output.FormatJSON:
		return output.PrintJSON(w, np)
	case output.FormatYAML:
		return output.PrintYAML(w, np)
	default:
	}

	bw := bufio.NewWriter(w)

	fmt.Fprintf(bw, "Name:         %s\n", np.Name)
	fmt.Fprintf(bw, "ID:           %s\n", ptrStr(np.Id))
	fmt.Fprintf(bw, "Cluster:      %s\n", ptrStr(np.OwnerReferences.Id))
	fmt.Fprintf(bw, "Generation:   %d\n", np.Generation)
	if np.Spec.Replicas != nil {
		fmt.Fprintf(bw, "Replicas:     %d\n", *np.Spec.Replicas)
	}
	fmt.Fprintf(bw, "Instance:     %s\n", ptrStr(np.Spec.Platform.Gcp.InstanceType))
	if np.Spec.Platform.Gcp.RootVolume != nil {
		if np.Spec.Platform.Gcp.RootVolume.Size != nil {
			fmt.Fprintf(bw, "Disk Size:    %d GB\n", *np.Spec.Platform.Gcp.RootVolume.Size)
		}
		if np.Spec.Platform.Gcp.RootVolume.Type != nil {
			fmt.Fprintf(bw, "Disk Type:    %s\n", *np.Spec.Platform.Gcp.RootVolume.Type)
		}
	}
	if np.Spec.Platform.Gcp.Zone != nil {
		fmt.Fprintf(bw, "Zone:         %s\n", *np.Spec.Platform.Gcp.Zone)
	}
	if np.Spec.Release != nil {
		if np.Spec.Release.Version != nil {
			fmt.Fprintf(bw, "Version:      %s\n", *np.Spec.Release.Version)
		}
		if np.Spec.Release.ChannelGroup != nil {
			fmt.Fprintf(bw, "Channel:      %s\n", *np.Spec.Release.ChannelGroup)
		}
	}
	fmt.Fprintf(bw, "Status:       %s\n", nodePoolStatusDetail(np))
	if !np.CreatedTime.IsZero() {
		fmt.Fprintf(bw, "CreatedAt:    %s\n", np.CreatedTime.Format("2006-01-02T15:04:05Z"))
	}
	if np.CreatedBy != "" {
		fmt.Fprintf(bw, "CreatedBy:    %s\n", string(np.CreatedBy))
	}
	if np.DeletedTime != nil {
		fmt.Fprintf(bw, "DeletedAt:    %s\n", np.DeletedTime.Format("2006-01-02T15:04:05Z"))
	}
	if np.DeletedBy != nil {
		fmt.Fprintf(bw, "DeletedBy:    %s\n", string(*np.DeletedBy))
	}

	if len(np.Status.Conditions) > 0 {
		fmt.Fprintln(bw, "\nConditions:")
		t := output.NewTable(bw, "TYPE", "STATUS", "GEN", "REASON", "MESSAGE")
		for _, cond := range np.Status.Conditions {
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

// nodePoolStatus returns a short human-friendly phase for table/list output.
func nodePoolStatus(np *hyperfleet.NodePool) string {
	phase, _ := deriveNodePoolStatus(np)
	return phase
}

// nodePoolStatusDetail returns a phase with parenthetical explanation for get output.
func nodePoolStatusDetail(np *hyperfleet.NodePool) string {
	phase, detail := deriveNodePoolStatus(np)
	if detail == "" {
		return phase
	}
	return fmt.Sprintf("%s (%s)", phase, detail)
}

func deriveNodePoolStatus(np *hyperfleet.NodePool) (phase, detail string) {
	if np.DeletedTime != nil {
		return "Deleting", ""
	}

	conditions := np.Status.Conditions
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
			return "Degraded", npConditionSummary(reconciled, np.Generation)
		}

		return "Progressing", ""
	}

	return "Progressing", ""
}

func npConditionSummary(cond *hyperfleet.ResourceCondition, generation int32) string {
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

func truncateID(id string) string {
	if len(id) > 12 {
		return id[:12] + "..."
	}
	return id
}

func releaseVersion(np *hyperfleet.NodePool) string {
	if np.Spec.Release != nil && np.Spec.Release.Version != nil && *np.Spec.Release.Version != "" {
		return *np.Spec.Release.Version
	}
	return "<none>"
}

func replicas(np *hyperfleet.NodePool) string {
	if np.Spec.Replicas != nil {
		return fmt.Sprintf("%d", *np.Spec.Replicas)
	}
	return "-"
}
