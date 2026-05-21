package wf

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/workflows"
	"github.com/spf13/cobra"
)

func newResumeCmd() *cobra.Command {
	var (
		data    string
		timeout time.Duration
		wait    bool
	)

	cmd := &cobra.Command{
		Use:   "resume <workflow> <execution-id>",
		Short: "Resume a paused workflow by triggering its callback",
		Long: `Resume a workflow execution that is waiting on a callback.

Fetches the pending callback URL for the execution and triggers it
with the provided data. Use 'gcphcp ops wf status' to see if an
execution has pending callbacks.

Examples:
  # Resume with approval data
  gcphcp ops wf resume approval-flow abc123-def456 --data '{"approved": true}'

  # Resume with empty payload
  gcphcp ops wf resume approval-flow abc123-def456

  # Resume and wait for completion
  gcphcp ops wf resume approval-flow abc123-def456 --data '{"approved": true}' --wait`,

		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			workflowName := args[0]
			execID := args[1]

			project, _ := cmd.Flags().GetString("project")
			region, _ := cmd.Flags().GetString("region")
			outputFormat, _ := cmd.Flags().GetString("output")

			if project == "" {
				return fmt.Errorf("--project is required (or set GCPHCP_PROJECT)")
			}
			if region == "" {
				return fmt.Errorf("--region is required (or set GCPHCP_REGION)")
			}

			execName := fmt.Sprintf("projects/%s/locations/%s/workflows/%s/executions/%s",
				project, region, workflowName, execID)

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			client, err := workflows.NewClient(ctx, project, region)
			if err != nil {
				return fmt.Errorf("creating client: %w", err)
			}
			defer client.Close()

			result, err := client.GetExecution(ctx, execName)
			if err != nil {
				return fmt.Errorf("getting execution status: %w", err)
			}
			if result.State != "ACTIVE" {
				return fmt.Errorf("execution is %s, not waiting on a callback", result.State)
			}

			callbacks, err := client.ListCallbacks(ctx, result.Name)
			if err != nil {
				return fmt.Errorf("listing callbacks: %w", err)
			}

			if len(callbacks) == 0 {
				return fmt.Errorf("execution is ACTIVE but has no pending callbacks")
			}

			cb := callbacks[0]

			var parsedData map[string]interface{}
			if data != "" {
				if err := json.Unmarshal([]byte(data), &parsedData); err != nil {
					return fmt.Errorf("parsing --data as JSON: %w", err)
				}
			}

			fmt.Fprintf(os.Stderr, "Triggering callback: %s %s\n", cb.Method, cb.URL)

			if err := client.TriggerCallback(ctx, cb.URL, cb.Method, parsedData); err != nil {
				return fmt.Errorf("triggering callback: %w", err)
			}

			fmt.Fprintf(os.Stderr, "Callback triggered. Workflow resuming.\n")

			if wait {
				fmt.Fprintf(os.Stderr, "Waiting for execution to complete...\n")
				result, err := client.WaitForCompletion(ctx, execName)
				if err != nil {
					return fmt.Errorf("waiting for execution: %w", err)
				}
				return printStatus(result, workflowName, execID, outputFormat)
			}

			fmt.Fprintf(os.Stderr, "\nCheck progress with:\n")
			fmt.Fprintf(os.Stderr, "  gcphcp ops wf status %s %s\n", workflowName, execID)

			return nil
		},
	}

	cmd.Flags().StringVar(&data, "data", "", "JSON data to send with the callback")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Maximum time to wait")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the execution to complete after resuming")

	return cmd
}
