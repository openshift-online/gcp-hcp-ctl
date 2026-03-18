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

func newLogsCmd() *cobra.Command {
	var (
		namespace string
		container string
		tailLines int
		previous  bool
		timeout   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "logs <pod-name>",
		Short: "Get pod logs via Cloud Workflows",
		Long: `Get Kubernetes pod logs from a GKE cluster using the logs workflow.
Works like kubectl logs but runs through Cloud Workflows.

Examples:
  # Get logs for a pod
  gcphcp ops logs kube-apiserver-abc123 -n clusters-test-pd-test-pd

  # Get logs from a specific container
  gcphcp ops logs my-pod -n default -c my-container

  # Get last 50 lines
  gcphcp ops logs my-pod -n default --tail 50

  # Get logs from previous container instance (crashloop debugging)
  gcphcp ops logs my-pod -n default --previous`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			podName := args[0]

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
				return fmt.Errorf("--namespace is required for logs")
			}

			data := map[string]interface{}{
				"namespace":  namespace,
				"pod":        podName,
				"tail_lines": tailLines,
			}
			if container != "" {
				data["container"] = container
			}
			if previous {
				data["previous"] = true
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			client, err := workflows.NewClient(ctx, project, region)
			if err != nil {
				return fmt.Errorf("creating client: %w", err)
			}
			defer client.Close()

			if err := checkPAMGate(ctx, client, "logs", cmd, os.Stderr); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Getting logs for %s", podName)
			if container != "" {
				fmt.Fprintf(os.Stderr, " (container: %s)", container)
			}
			fmt.Fprintf(os.Stderr, " in %s\n", namespace)
			if previous {
				fmt.Fprintf(os.Stderr, "Previous container instance\n")
			}

			_, result, err := client.Run(ctx, "logs", data)
			if err != nil {
				return fmt.Errorf("executing workflow: %w", err)
			}

			if result.State == "FAILED" {
				return fmt.Errorf("workflow failed: %s", result.Error)
			}

			format := output.ParseFormat(outputFormat)
			if format == output.FormatJSON {
				return output.PrintJSON(os.Stdout, result.Result)
			}

			if status, _ := result.Result["status"].(string); status == "container_required" {
				fmt.Fprintf(os.Stderr, "Error: pod %q has multiple containers; you must specify one:\n", podName)
				if containers, ok := result.Result["available_containers"].([]interface{}); ok {
					for _, c := range containers {
						fmt.Fprintf(os.Stderr, "  - %v\n", c)
					}
				}
				fmt.Fprintf(os.Stderr, "\nUse: gcphcp ops logs %s -n %s -c <container>\n", podName, namespace)
				return fmt.Errorf("container name required")
			}

			if logs, ok := result.Result["logs"]; ok {
				fmt.Fprintln(os.Stdout, logs)
			} else {
				return output.PrintJSON(os.Stdout, result.Result)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Kubernetes namespace (required)")
	cmd.Flags().StringVarP(&container, "container", "c", "", "Container name")
	cmd.Flags().IntVar(&tailLines, "tail", 100, "Number of log lines to retrieve")
	cmd.Flags().BoolVar(&previous, "previous", false, "Get logs from previous container instance")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Maximum time to wait for workflow completion")

	return cmd
}
