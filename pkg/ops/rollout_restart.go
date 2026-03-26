package ops

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ckandag/gcp-hcp-cli/pkg/gcp/workflows"
	"github.com/ckandag/gcp-hcp-cli/pkg/output"
	"github.com/spf13/cobra"
)

func newRolloutRestartCmd() *cobra.Command {
	var (
		namespace string
		timeout   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "rollout-restart <resource-type> <name>",
		Short: "Rolling restart of a Kubernetes workload via Cloud Workflows",
		Long: `Perform a rolling restart of a Kubernetes workload by patching the
pod template annotation kubectl.kubernetes.io/restartedAt (same mechanism
as kubectl rollout restart).

Supported resource types: deployments, statefulsets, daemonsets.

Examples:
  # Restart a deployment
  gcphcp ops rollout-restart deployments operator -n hypershift

  # Restart a statefulset
  gcphcp ops rollout-restart statefulsets etcd -n clusters-abc123

  # Short aliases work too
  gcphcp ops rollout-restart deploy operator -n hypershift
  gcphcp ops rollout-restart sts etcd -n clusters-abc123`,

		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resourceType := args[0]
			if expanded, ok := resourceTypeExpand[resourceType]; ok {
				resourceType = expanded
			}
			resourceName := args[1]

			project, _ := cmd.Flags().GetString("project")
			region, _ := cmd.Flags().GetString("region")
			outputFormat, _ := cmd.Flags().GetString("output")

			if project == "" {
				return fmt.Errorf("--project is required (or set GCPHCP_PROJECT)")
			}
			if region == "" {
				return fmt.Errorf("--region is required (or set GCPHCP_REGION)")
			}
			if namespace == "" {
				return fmt.Errorf("--namespace is required")
			}

			data := map[string]interface{}{
				"resource_type": resourceType,
				"namespace":     namespace,
				"name":          resourceName,
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			client, err := workflows.NewClient(ctx, project, region)
			if err != nil {
				return fmt.Errorf("creating client: %w", err)
			}
			defer client.Close()

			if err := checkPAMGate(ctx, client, "rollout", cmd, os.Stderr); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Rolling restart %s %s (ns: %s)\n", resourceType, resourceName, namespace)

			_, result, err := client.Run(ctx, "rollout", data)
			if err != nil {
				return fmt.Errorf("executing workflow: %w", err)
			}

			format := output.ParseFormat(outputFormat)
			if format == output.FormatJSON {
				return output.PrintJSON(os.Stdout, result.Result)
			}

			status := output.GetString(result.Result, "status")
			if status == "error" {
				errMsg := output.GetString(result.Result, "error")
				return fmt.Errorf("failed to rollout restart %s/%s: %s", resourceType, resourceName, errMsg)
			}

			restartedAt := output.GetString(result.Result, "restarted_at")
			fmt.Fprintf(os.Stdout, "%s \"%s\" rollout restart triggered (restarted_at: %s)\n", resourceType, resourceName, restartedAt)
			return nil
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Kubernetes namespace (required)")
	_ = cmd.MarkFlagRequired("namespace")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Maximum time to wait")

	return cmd
}
