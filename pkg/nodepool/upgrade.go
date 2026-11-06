package nodepool

import (
	"fmt"
	"strings"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/internal/access"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilversion "k8s.io/apimachinery/pkg/util/version"
)

type upgradeOptions struct {
	clusterRef string
	version    string
	start      string
	outputFmt  string
}

func newUpgradeCmd() *cobra.Command {
	opts := &upgradeOptions{}
	cmd := &cobra.Command{
		Use:   "upgrade <nodepool-name>",
		Short: "Request a one-time NodePool upgrade",
		Long: `Create a one-time NodePool upgrade request. The request is processed asynchronously.

  gcphcpctl nodepool upgrade workers --cluster my-cluster --version 4.22.17
  gcphcpctl nodepool upgrade workers --cluster my-cluster --version 4.22.17 \
    --start 2026-10-21T02:00:00Z

Use "gcphcpctl nodepool upgrade status <request-name> --cluster <cluster-name>"
to check whether the request was accepted and the rollout completed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			request, err := opts.buildRequest(args[0], clientFromCmd(cmd).Namespace())
			if err != nil {
				return err
			}
			client := clientFromCmd(cmd)
			created, err := client.NodePoolUpgradeRequests().Create(cmd.Context(), client.Namespace(), opts.clusterRef, request)
			if err != nil {
				return fmt.Errorf("requesting NodePool upgrade: %w", err)
			}
			if output.ParseFormat(opts.outputFmt) != output.FormatText {
				return access.Structured(cmd, created)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"NodePool upgrade request %s created for %s (target %s).\nCheck status with: gcphcpctl nodepool upgrade status %s --cluster %s\n",
				created.Name, args[0], opts.version, created.Name, opts.clusterRef)
			return err
		},
	}
	cmd.Flags().StringVar(&opts.clusterRef, "cluster", "", "Cluster name (required)")
	cmd.Flags().StringVar(&opts.version, "version", "", "Target OpenShift version (required)")
	cmd.Flags().StringVar(&opts.start, "start", "", "Earliest start time in RFC3339 format (optional)")
	cmd.Flags().StringVarP(&opts.outputFmt, "output", "o", "text", "Output format: text, json, yaml")
	_ = cmd.MarkFlagRequired("cluster")
	_ = cmd.MarkFlagRequired("version")
	cmd.AddCommand(newUpgradeStatusCmd())
	return cmd
}

func (o *upgradeOptions) buildRequest(nodePoolName, namespace string) (*platformapi.NodePoolUpgradeRequest, error) {
	if strings.TrimSpace(nodePoolName) == "" || strings.TrimSpace(o.clusterRef) == "" {
		return nil, fmt.Errorf("nodepool name and --cluster are required")
	}
	if _, err := utilversion.ParseSemantic(o.version); err != nil {
		return nil, fmt.Errorf("--version must be a semantic OpenShift version: %w", err)
	}
	var startTime *metav1.Time
	if o.start != "" {
		parsed, err := time.Parse(time.RFC3339, o.start)
		if err != nil {
			return nil, fmt.Errorf("--start must be an RFC3339 timestamp: %w", err)
		}
		converted := metav1.NewTime(parsed.UTC())
		startTime = &converted
	}
	return &platformapi.NodePoolUpgradeRequest{
		TypeMeta: metav1.TypeMeta{APIVersion: gcpv1.GroupVersion.String(), Kind: "NodePoolUpgradeRequest"},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "nodepool-upgrade-",
			Namespace:    namespace,
		},
		Spec: platformapi.NodePoolUpgradeRequestSpec{
			ClusterID: o.clusterRef, NodePoolID: nodePoolName, TargetVersion: o.version, StartTime: startTime,
		},
	}, nil
}

func newUpgradeStatusCmd() *cobra.Command {
	var clusterRef, outputFmt string
	cmd := &cobra.Command{
		Use:   "status <request-name>",
		Short: "Show the status of a NodePool upgrade request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := clientFromCmd(cmd)
			request, err := client.NodePoolUpgradeRequests().Get(cmd.Context(), client.Namespace(), clusterRef, args[0])
			if err != nil {
				return fmt.Errorf("getting NodePool upgrade request: %w", err)
			}
			if output.ParseFormat(outputFmt) != output.FormatText {
				return access.Structured(cmd, request)
			}
			return printUpgradeStatus(cmd, request)
		},
	}
	cmd.Flags().StringVar(&clusterRef, "cluster", "", "Cluster name (required)")
	cmd.Flags().StringVarP(&outputFmt, "output", "o", "text", "Output format: text, json, yaml")
	_ = cmd.MarkFlagRequired("cluster")
	return cmd
}

func printUpgradeStatus(cmd *cobra.Command, request *platformapi.NodePoolUpgradeRequest) error {
	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(w, "Name:       %s\nCluster:    %s\nNodePool:   %s\nTarget:     %s\n",
		request.Name, request.Spec.ClusterID, request.Spec.NodePoolID, request.Spec.TargetVersion); err != nil {
		return err
	}
	if request.Status == nil {
		return nil
	}
	for _, entry := range []struct {
		label string
		time  *metav1.Time
	}{
		{"Accepted:   ", request.Status.AcceptedTime},
		{"Started:    ", request.Status.StartedTime},
		{"Completed:  ", request.Status.CompletedTime},
	} {
		if entry.time != nil {
			if _, err := fmt.Fprintf(w, "%s%s\n", entry.label, entry.time.UTC().Format(time.RFC3339)); err != nil {
				return err
			}
		}
	}
	if len(request.Status.Conditions) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "\nConditions:"); err != nil {
		return err
	}
	table := output.NewTable(w, "TYPE", "STATUS", "REASON", "MESSAGE")
	for _, condition := range request.Status.Conditions {
		table.AddRow(condition.Type, string(condition.Status), condition.Reason, condition.Message)
	}
	return table.Flush()
}
