package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/auth"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/hyperfleet"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/kubeconfig"
	"github.com/spf13/cobra"
)

type loginOptions struct {
	server         string
	kubeconfigPath string
	namespace      string
	insecureSkipTLS bool
}

func newLoginCmd() *cobra.Command {
	opts := &loginOptions{}

	cmd := &cobra.Command{
		Use:   "login <cluster-name-or-id>",
		Short: "Log in to a hosted cluster",
		Long: `Configures kubectl to access a hosted cluster by setting up a kubeconfig
context with gcloud exec-based authentication.

The command resolves the cluster's API endpoint from the HyperFleet API
(via hc-adapter status data), or uses the explicit --server flag as a fallback.`,
		Example: `  # Login using the cluster name (endpoint resolved from API)
  gcphcpctl cluster login my-cluster

  # Login with an explicit API server
  gcphcpctl cluster login my-cluster --server https://api.my-cluster.example.com

  # Login to a custom kubeconfig file
  gcphcpctl cluster login my-cluster --kubeconfig /tmp/my-kubeconfig`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogin(cmd.Context(), cmd, args[0], opts)
		},
	}

	cmd.Flags().StringVar(&opts.server, "server", "", "API server URL (overrides API lookup)")
	cmd.Flags().StringVar(&opts.kubeconfigPath, "kubeconfig", "", "Path to kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")
	cmd.Flags().StringVar(&opts.namespace, "namespace", "default", "Default namespace for the context")
	cmd.Flags().BoolVar(&opts.insecureSkipTLS, "insecure-skip-tls-verify", true, "Skip TLS certificate verification")

	return cmd
}

func runLogin(ctx context.Context, cmd *cobra.Command, clusterRef string, opts *loginOptions) error {
	apiEndpoint, _ := cmd.Flags().GetString("api-endpoint")

	server := opts.server
	clusterName := clusterRef

	if server == "" {
		resolved, name, err := resolveClusterEndpoint(ctx, apiEndpoint, clusterRef)
		if err != nil {
			return fmt.Errorf("resolving cluster endpoint: %w (use --server to specify the API server directly)", err)
		}
		server = resolved
		if name != "" {
			clusterName = name
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Cluster:  %s\n", clusterName)
	fmt.Fprintf(cmd.OutOrStdout(), "Server:   %s\n", server)

	contextName, err := kubeconfig.Update(kubeconfig.UpdateOptions{
		ClusterName:     clusterName,
		Server:          server,
		Namespace:       opts.namespace,
		InsecureSkipTLS: opts.insecureSkipTLS,
		KubeconfigPath:  opts.kubeconfigPath,
	})
	if err != nil {
		return fmt.Errorf("updating kubeconfig: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Context:  %s\n", contextName)

	validateCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	whoami, err := kubeconfig.ValidateAccess(validateCtx, opts.kubeconfigPath, contextName)
	if err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "\nWarning: access validation failed: %v\n", err)
		fmt.Fprintf(cmd.OutOrStdout(), "  The kubeconfig was written, but cluster access could not be verified.\n")
		fmt.Fprintf(cmd.OutOrStdout(), "  You may need to check your RBAC permissions or gcloud authentication.\n")
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", whoami)
	fmt.Fprintf(cmd.OutOrStdout(), "\nLogged in to %q successfully.\n", clusterName)
	return nil
}

// adapterStatusList is the response shape of GET /clusters/{id}/statuses.
type adapterStatusList struct {
	Items []adapterStatusItem `json:"items"`
}

type adapterStatusItem struct {
	Adapter string         `json:"adapter"`
	Data    map[string]any `json:"data,omitempty"`
}

// resolveClusterEndpoint queries the HyperFleet API for the cluster's API
// server endpoint. It first resolves the cluster by name/ID, then fetches
// the hc-adapter status data to extract the hostedCluster.apiEndpoint.
// Returns (endpoint, clusterName, error).
func resolveClusterEndpoint(ctx context.Context, apiEndpoint, clusterRef string) (string, string, error) {
	tokenSource := auth.NewTokenSource()
	client, err := newClientWithTokenSource(apiEndpoint, tokenSource)
	if err != nil {
		return "", "", err
	}

	cluster, err := resolveCluster(ctx, client, clusterRef)
	if err != nil {
		return "", "", err
	}

	clusterID := ptrStr(cluster.Id)
	clusterName := cluster.Name
	if clusterID == "" {
		return "", clusterName, fmt.Errorf("cluster %q has no ID", clusterRef)
	}

	endpoint, err := fetchHCAdapterEndpoint(ctx, apiEndpoint, clusterID, tokenSource)
	if err != nil {
		return "", clusterName, err
	}
	return endpoint, clusterName, nil
}

// resolveCluster looks up a cluster by name or ID. It first tries a
// direct ID lookup, and if that fails with 404, searches by name.
func resolveCluster(ctx context.Context, client *hyperfleet.ClientWithResponses, ref string) (*hyperfleet.Cluster, error) {
	resp, err := client.GetClusterByIdWithResponse(ctx, ref, nil)
	if err != nil {
		return nil, fmt.Errorf("looking up cluster %q: %w", ref, err)
	}
	if resp.JSON200 != nil {
		return resp.JSON200, nil
	}

	listResp, err := client.GetClustersWithResponse(ctx, nil)
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
	return nil, fmt.Errorf("cluster %q not found", ref)
}

// fetchHCAdapterEndpoint calls the /statuses endpoint directly to get
// the hc-adapter's data.hostedCluster.apiEndpoint field.
func fetchHCAdapterEndpoint(ctx context.Context, apiEndpoint, clusterID string, tokenSource *auth.TokenSource) (string, error) {
	url := strings.TrimRight(apiEndpoint, "/") + "/clusters/" + clusterID + "/statuses"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	token, _, err := tokenSource.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("getting auth token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching statuses: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("statuses endpoint returned HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading statuses response: %w", err)
	}

	return extractEndpointFromBody(body)
}

// extractEndpointFromBody parses a statuses JSON response and extracts
// the hc-adapter's hostedCluster.apiEndpoint value.
func extractEndpointFromBody(body []byte) (string, error) {
	var statusList adapterStatusList
	if err := json.Unmarshal(body, &statusList); err != nil {
		return "", fmt.Errorf("parsing statuses response: %w", err)
	}

	for _, item := range statusList.Items {
		if item.Adapter != "hc-adapter" {
			continue
		}

		hc, ok := item.Data["hostedCluster"].(map[string]any)
		if !ok {
			continue
		}
		endpoint, ok := hc["apiEndpoint"].(string)
		if ok && strings.HasPrefix(endpoint, "https://") {
			return endpoint, nil
		}
	}

	return "", fmt.Errorf("no API endpoint found in hc-adapter status data (cluster may still be provisioning)")
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
