package ops

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/workflows"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	"github.com/spf13/cobra"
)

func newDeleteCmd() *cobra.Command {
	var (
		namespace    string
		gracePeriod  int
		timeout      time.Duration
	)

	cmd := &cobra.Command{
		Use:   "delete <resource-type> <name>",
		Short: "Delete a Kubernetes resource via Cloud Workflows",
		Long: `Delete a Kubernetes resource by type and name.
Supported resource types: pods, jobs, deployments.

Examples:
  # Delete a pod
  gcphcpctl ops delete pods my-pod -n clusters-abc123

  # Delete a deployment
  gcphcpctl ops delete deployments my-deploy -n clusters-abc123

  # Force delete a pod (grace period 0)
  gcphcpctl ops delete pods my-pod -n clusters-abc123 --grace-period 0

  # Short aliases work too
  gcphcpctl ops delete po my-pod -n clusters-abc123`,

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


			if namespace == "" {
				return fmt.Errorf("--namespace is required")
			}

			data := map[string]interface{}{
				"resource_type": resourceType,
				"namespace":     namespace,
				"name":          resourceName,
			}
			if cmd.Flags().Changed("grace-period") {
				data["grace_period_seconds"] = gracePeriod
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			client, err := workflows.NewClient(ctx, project, region)
			if err != nil {
				return fmt.Errorf("creating client: %w", err)
			}
			defer client.Close()

			if err := checkPAMGate(ctx, client, "delete", cmd, os.Stderr); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Deleting %s %s (ns: %s)\n", resourceType, resourceName, namespace)

			_, result, err := client.Run(ctx, "delete", data)
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
				return fmt.Errorf("failed to delete %s/%s: %s", resourceType, resourceName, errMsg)
			}

			fmt.Fprintf(os.Stdout, "%s \"%s\" deleted\n", resourceType, resourceName)
			return nil
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Kubernetes namespace (required)")
	_ = cmd.MarkFlagRequired("namespace")
	cmd.Flags().IntVar(&gracePeriod, "grace-period", 30, "Grace period in seconds before force kill (max 300)")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Maximum time to wait")

	return cmd
}
