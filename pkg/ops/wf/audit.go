package wf

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ckandag/gcp-hcp-cli/pkg/gcp/auditlog"
	"github.com/ckandag/gcp-hcp-cli/pkg/output"
	"github.com/spf13/cobra"
)

func newAuditCmd() *cobra.Command {
	var (
		timeout   time.Duration
		limit     int
		freshness time.Duration
	)

	cmd := &cobra.Command{
		Use:   "audit [workflow]",
		Short: "Show who triggered workflow executions",
		Long: `Query Cloud Audit Logs to show who triggered workflow executions.

Without a workflow name, shows audit entries for all workflows.
With a workflow name, filters to that specific workflow.

Examples:
  # Show recent audit entries for all workflows
  gcphcp ops wf audit

  # Show audit entries for the 'get' workflow
  gcphcp ops wf audit get

  # JSON output
  gcphcp ops wf audit get -o json

  # Look back 30 days, show up to 50 entries
  gcphcp ops wf audit --freshness 720h --limit 50`,

		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _ := cmd.Flags().GetString("project")
			region, _ := cmd.Flags().GetString("region")
			outputFormat, _ := cmd.Flags().GetString("output")

			if project == "" {
				return fmt.Errorf("--project is required (or set GCPHCP_PROJECT)")
			}
			if region == "" {
				return fmt.Errorf("--region is required (or set GCPHCP_REGION)")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			client, err := auditlog.NewClient(ctx, project)
			if err != nil {
				return fmt.Errorf("creating audit log client: %w", err)
			}

			opts := auditlog.QueryOptions{
				Region:    region,
				Freshness: freshness,
				Limit:     limit,
			}
			if len(args) == 1 {
				opts.Workflow = args[0]
			}

			entries, err := client.QueryWorkflowAuditLogs(ctx, opts)
			if err != nil {
				return fmt.Errorf("querying audit logs: %w", err)
			}

			format := output.ParseFormat(outputFormat)
			if format == output.FormatJSON {
				return output.PrintJSON(os.Stdout, entries)
			}

			if len(entries) == 0 {
				fmt.Fprintln(os.Stdout, "No audit entries found.")
				return nil
			}

			t := output.NewTable(os.Stdout, "TIMESTAMP", "USER", "WORKFLOW", "EXECUTION_ID")
			for _, e := range entries {
				ts := e.Timestamp.Format("2006-01-02 15:04:05")
				t.AddRow(ts, e.User, e.Workflow, e.ExecutionID)
			}
			return t.Flush()
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Maximum time to wait for API response")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum number of audit entries to show")
	cmd.Flags().DurationVar(&freshness, "freshness", 168*time.Hour, "How far back to query (default 7 days)")

	return cmd
}
