package cluster

import (
	"fmt"
	"io"
	"net/http"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/auth"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/hyperfleet"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	"github.com/spf13/cobra"
)

// NewClusterCmd returns the "cluster" command group.
func NewClusterCmd() *cobra.Command {
	var clusterCmd *cobra.Command
	clusterCmd = &cobra.Command{
		Use:   "cluster",
		Short: "Manage HyperFleet clusters",
		Long:  `Create, get, list, delete, and login to HyperFleet clusters via the HyperFleet API.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if parent := clusterCmd.Parent(); parent != nil && parent.PersistentPreRunE != nil {
				if err := parent.PersistentPreRunE(cmd, args); err != nil {
					return err
				}
			}
			return validateRequiredFlags(cmd)
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

func formatError(resp *http.Response, body []byte) string {
	msg := string(body)
	if len(msg) > 500 {
		msg = msg[:500] + "..."
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
	if format == "json" {
		return output.PrintJSON(w, c)
	}

	fmt.Fprintf(w, "Name:       %s\n", c.Name)
	fmt.Fprintf(w, "ID:         %s\n", ptrStr(c.Id))
	fmt.Fprintf(w, "Kind:       %s\n", ptrStr(c.Kind))
	fmt.Fprintf(w, "Generation: %d\n", c.Generation)
	fmt.Fprintf(w, "Platform:   %s (%s)\n", c.Spec.Platform.Type, c.Spec.Platform.Gcp.Region)
	fmt.Fprintf(w, "Project:    %s\n", c.Spec.Platform.Gcp.ProjectID)
	if c.Spec.Release != nil && c.Spec.Release.Version != nil {
		fmt.Fprintf(w, "Version:    %s\n", *c.Spec.Release.Version)
	}
	if c.Spec.IssuerURL != nil && *c.Spec.IssuerURL != "" {
		fmt.Fprintf(w, "IssuerURL:  %s\n", *c.Spec.IssuerURL)
	}
	fmt.Fprintf(w, "Status:     %s\n", clusterStatus(c))
	if !c.CreatedTime.IsZero() {
		fmt.Fprintf(w, "Created:    %s\n", c.CreatedTime.Format("2006-01-02T15:04:05Z"))
	}
	if c.CreatedBy != "" {
		fmt.Fprintf(w, "CreatedBy:  %s\n", string(c.CreatedBy))
	}

	if len(c.Status.Conditions) > 0 {
		fmt.Fprintln(w, "\nConditions:")
		t := output.NewTable(w, "TYPE", "STATUS", "REASON", "MESSAGE")
		for _, cond := range c.Status.Conditions {
			msg := ptrStr(cond.Message)
			if len(msg) > 80 {
				msg = msg[:80] + "..."
			}
			t.AddRow(cond.Type, string(cond.Status), ptrStr(cond.Reason), msg)
		}
		_ = t.Flush()
	}

	return nil
}

func clusterStatus(c *hyperfleet.Cluster) string {
	if len(c.Status.Conditions) == 0 {
		return "Pending"
	}
	for _, cond := range c.Status.Conditions {
		if cond.Type == "Reconciled" {
			if cond.Status == "True" {
				return "Ready"
			}
			return ptrStr(cond.Reason)
		}
	}
	return "Progressing"
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
