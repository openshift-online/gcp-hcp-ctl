package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/cloudrun"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	"github.com/spf13/cobra"
)

func newDiagnoseCmd() *cobra.Command {
	var (
		serviceName string
		timeout     time.Duration
	)

	cmd := &cobra.Command{
		Use:   "diagnose <query>",
		Short: "Run AI-powered cluster diagnostics via the diagnose-agent",
		Long: `Send a natural language query to the diagnose-agent Cloud Run service
for AI-powered cluster diagnostics. The agent uses Vertex AI to investigate
cluster issues and returns a structured diagnosis.

The diagnose-agent service URL is auto-discovered from the configured
project and region. Use --service-name to override the default service name.

Examples:
  # Diagnose a crashlooping pod
  gcphcp ops diagnose "why is pod etcd-0 crashlooping in namespace clusters-foo"

  # Investigate a node issue
  gcphcp ops diagnose "node gke-abc123 is NotReady, what's wrong?"

  # Get JSON output for scripting
  gcphcp ops diagnose "check health of namespace hypershift" -o json

  # Use a custom service name
  gcphcp ops diagnose "why are pods failing" --service-name my-diagnose-agent`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]

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

			client := cloudrun.NewClient(ctx, project, region)

			fmt.Fprintf(os.Stderr, "Discovering diagnose-agent service in %s/%s...\n", project, region)
			serviceURL, err := client.DiscoverServiceURL(ctx, serviceName)
			if err != nil {
				return fmt.Errorf("discovering service: %w", err)
			}

			fmt.Fprintf(os.Stderr, "Sending query to diagnose-agent...\n")
			fmt.Fprintf(os.Stderr, "  Query: %s\n\n", query)

			format := output.ParseFormat(outputFormat)

			step := 0
			resp, err := client.DiagnoseStream(ctx, serviceURL, query, func(event cloudrun.StreamEvent) {
				if format == output.FormatJSON {
					return
				}
				switch event.Event {
				case "tool_call":
					step++
					desc := formatToolCall(event.Tool, event.Parameters)
					fmt.Fprintf(os.Stderr, "  [%d] %s\n", step, desc)
				case "tool_result":
					result := unquoteResult(event.Result)
					if len(result) > 80 {
						result = result[:80] + "..."
					}
					fmt.Fprintf(os.Stderr, "      -> %s\n", result)
				}
			})
			if err != nil {
				return fmt.Errorf("diagnose failed: %w", err)
			}

			if resp.Error != "" {
				return fmt.Errorf("diagnose-agent error: %s", resp.Error)
			}

			fmt.Fprintln(os.Stderr)

			if format == output.FormatJSON {
				return output.PrintJSON(os.Stdout, resp)
			}

			return output.PrintDiagnosis(os.Stdout, resp.Diagnosis.RootCause, resp.Diagnosis.Confidence,
				resp.Diagnosis.Severity, resp.Diagnosis.Evidence, resp.Diagnosis.Recommendation,
				resp.Metadata)
		},
	}

	cmd.Flags().StringVar(&serviceName, "service-name", "diagnose-agent", "Cloud Run service name for the diagnose-agent")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "Maximum time to wait for diagnosis")

	return cmd
}

func formatToolCall(tool string, params map[string]interface{}) string {
	switch tool {
	case "get_resources":
		rt := paramStr(params, "resource_type")
		ns := paramStr(params, "namespace")
		name := paramStr(params, "name")
		s := fmt.Sprintf("Getting %s", rt)
		if name != "" {
			s += fmt.Sprintf(" %s", name)
		}
		if ns != "" {
			s += fmt.Sprintf(" in %s", ns)
		}
		return s
	case "get_logs":
		pod := paramStr(params, "pod")
		ns := paramStr(params, "namespace")
		return fmt.Sprintf("Getting logs for %s in %s", pod, ns)
	case "describe_resource":
		rt := paramStr(params, "resource_type")
		name := paramStr(params, "name")
		ns := paramStr(params, "namespace")
		s := fmt.Sprintf("Describing %s %s", rt, name)
		if ns != "" {
			s += fmt.Sprintf(" in %s", ns)
		}
		return s
	default:
		return fmt.Sprintf("Calling %s", tool)
	}
}

// unquoteResult extracts a clean string from a json.RawMessage result.
// The result may be a JSON string (quoted with escapes) or a JSON object/array.
func unquoteResult(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if len(s) == 0 {
		return ""
	}
	// Try to unquote as a JSON string (handles \" -> " etc).
	var unquoted string
	if err := json.Unmarshal(raw, &unquoted); err == nil {
		return unquoted
	}
	return s
}

func paramStr(params map[string]interface{}, key string) string {
	if v, ok := params[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}
