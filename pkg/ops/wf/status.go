package wf

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/workflows"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var (
		wait    bool
		timeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "status <workflow> <execution-id>",
		Short: "Check the status of a workflow execution",
		Long: `Check the status of a workflow execution by its ID.

Use this to check on workflows started with --async, or after detaching
from a running workflow with Ctrl+C.

Use --wait to block until the execution completes.

Examples:
  # Check status of an execution
  gcphcp ops wf status get abc123-def456

  # Wait for an execution to complete
  gcphcp ops wf status get abc123-def456 --wait

  # JSON output
  gcphcp ops wf status describe abc123-def456 -o json`,

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

			if wait {
				fmt.Fprintf(os.Stderr, "Waiting for execution %s to complete...\n", execID)
				result, err := client.WaitForCompletion(ctx, execName)
				if err != nil {
					return fmt.Errorf("waiting for execution: %w", err)
				}
				return printStatus(result, workflowName, execID, outputFormat)
			}

			result, err := client.GetExecution(ctx, execName)
			if err != nil {
				return fmt.Errorf("getting execution status: %w", err)
			}

			if result.State == "ACTIVE" {
				callbacks, cbErr := client.ListCallbacks(ctx, result.Name)
				if cbErr == nil {
					result.Callbacks = callbacks
				}
			}

			return printStatus(result, workflowName, execID, outputFormat)
		},
	}

	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the execution to complete")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Maximum time to wait")

	return cmd
}

func printStatus(result *workflows.ExecutionResult, workflowName, execID, outputFormat string) error {
	format := output.ParseFormat(outputFormat)

	if format == output.FormatJSON {
		data := map[string]interface{}{
			"state":      result.State,
			"start_time": result.StartTime.Format(time.RFC3339),
			"end_time":   result.EndTime.Format(time.RFC3339),
			"duration":   result.Duration.String(),
			"error":      result.Error,
			"result":     result.Result,
		}
		if len(result.Callbacks) > 0 {
			data["callbacks"] = result.Callbacks
		}
		return output.PrintJSON(os.Stdout, data)
	}

	stateDisplay := result.State
	if result.State == "ACTIVE" && len(result.Callbacks) > 0 {
		stateDisplay = "ACTIVE (waiting on callback)"
	}

	fmt.Fprintf(os.Stdout, "Workflow:   %s\n", workflowName)
	fmt.Fprintf(os.Stdout, "State:      %s\n", stateDisplay)
	fmt.Fprintf(os.Stdout, "Started:    %s (%s ago)\n",
		result.StartTime.Format("2006-01-02 15:04:05 UTC"),
		output.Age(result.StartTime.Format(time.RFC3339)))

	if !result.EndTime.IsZero() {
		fmt.Fprintf(os.Stdout, "Ended:      %s\n", result.EndTime.Format("2006-01-02 15:04:05 UTC"))
		fmt.Fprintf(os.Stdout, "Duration:   %s\n", result.Duration.Round(time.Millisecond))
	}

	if result.Error != "" {
		fmt.Fprintf(os.Stdout, "Error:      %s\n", result.Error)
	}

	if result.Result != nil && result.State == "SUCCEEDED" {
		fmt.Fprintf(os.Stdout, "Args:       %s\n", buildArgsSummary(result.Result))
	}

	if len(result.Callbacks) > 0 {
		fmt.Fprintf(os.Stdout, "\nCallbacks:\n")
		for _, cb := range result.Callbacks {
			fmt.Fprintf(os.Stdout, "  %s %s\n", cb.Method, cb.URL)
		}
		fmt.Fprintf(os.Stdout, "\nResume with:\n")
		fmt.Fprintf(os.Stdout, "  gcphcp ops wf resume %s %s --data '{\"approved\": true}'\n", workflowName, execID)
	}

	if result.State == "SUCCEEDED" || result.State == "FAILED" {
		fmt.Fprintf(os.Stdout, "\nUse -o json for full result.\n")
	}

	return nil
}

func buildArgsSummary(data map[string]interface{}) string {
	var parts []string

	if rt, ok := data["resource_type"].(string); ok {
		parts = append(parts, rt)
	}
	if pod, ok := data["pod"].(string); ok && pod != "" {
		parts = append(parts, pod)
	}
	if name, ok := data["name"].(string); ok && name != "" {
		parts = append(parts, name)
	}
	if ns, ok := data["namespace"].(string); ok && ns != "" {
		parts = append(parts, fmt.Sprintf("-n %s", ns))
	}
	if count, ok := data["count"]; ok {
		parts = append(parts, fmt.Sprintf("(%v items)", count))
	}
	if logs, ok := data["logs"].(string); ok {
		lines := 0
		for _, c := range logs {
			if c == '\n' {
				lines++
			}
		}
		parts = append(parts, fmt.Sprintf("(%d lines)", lines))
	}

	if len(parts) == 0 {
		return "ok"
	}
	return strings.Join(parts, " ")
}
