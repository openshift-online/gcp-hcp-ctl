package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ckandag/gcp-hcp-cli/pkg/gcp/workflows"
	"github.com/ckandag/gcp-hcp-cli/pkg/output"
	"github.com/spf13/cobra"
)

func newEtcdCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "etcd",
		Short: "etcd operations for HCP clusters",
		Long: `Run etcd operations on HCP clusters via Cloud Workflows.

These commands run etcdctl against the etcd cluster in the specified
HCP namespace (clusters-*).

Examples:
  gcphcp ops etcd health -n clusters-abc123
  gcphcp ops etcd status -n clusters-abc123
  gcphcp ops etcd member-list -n clusters-abc123
  gcphcp ops etcd defrag -n clusters-abc123
  gcphcp ops etcd compact -n clusters-abc123`,
	}

	cmd.AddCommand(newEtcdHealthCmd())
	cmd.AddCommand(newEtcdStatusCmd())
	cmd.AddCommand(newEtcdMemberListCmd())
	cmd.AddCommand(newEtcdDefragCmd())
	cmd.AddCommand(newEtcdCompactCmd())

	return cmd
}

func newEtcdHealthCmd() *cobra.Command {
	var (
		namespace string
		timeout   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check etcd endpoint health",
		Long: `Check the health of all etcd endpoints in an HCP cluster.

Examples:
  gcphcp ops etcd health -n clusters-abc123
  gcphcp ops etcd health -n clusters-abc123 -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEtcdCommand(cmd, "etcd-health", namespace, timeout, func(format output.Format, result map[string]interface{}) error {
				if format == output.FormatJSON {
					return output.PrintJSON(os.Stdout, result)
				}
				return output.PrintTable(os.Stdout, parseEtcdOutput(result), etcdHealthColumns)
			})
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "HCP namespace (required)")
	_ = cmd.MarkFlagRequired("namespace")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Maximum time to wait")

	return cmd
}

func newEtcdStatusCmd() *cobra.Command {
	var (
		namespace string
		timeout   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show etcd endpoint status",
		Long: `Show detailed status of all etcd endpoints including DB size,
raft index, leader info, and version.

Examples:
  gcphcp ops etcd status -n clusters-abc123
  gcphcp ops etcd status -n clusters-abc123 -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEtcdCommand(cmd, "etcd-status", namespace, timeout, func(format output.Format, result map[string]interface{}) error {
				if format == output.FormatJSON {
					return output.PrintJSON(os.Stdout, result)
				}
				return output.PrintTable(os.Stdout, parseEtcdOutput(result), etcdStatusColumns)
			})
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "HCP namespace (required)")
	_ = cmd.MarkFlagRequired("namespace")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Maximum time to wait")

	return cmd
}

func newEtcdMemberListCmd() *cobra.Command {
	var (
		namespace string
		timeout   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "member-list",
		Short: "List etcd cluster members",
		Long: `List all members of the etcd cluster with their IDs, names,
peer URLs, and client URLs.

Examples:
  gcphcp ops etcd member-list -n clusters-abc123
  gcphcp ops etcd member-list -n clusters-abc123 -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEtcdCommand(cmd, "etcd-member-list", namespace, timeout, func(format output.Format, result map[string]interface{}) error {
				if format == output.FormatJSON {
					return output.PrintJSON(os.Stdout, result)
				}
				parsed := parseEtcdOutput(result)
				// member-list returns {header, members}, extract the members array
				if m, ok := parsed.(map[string]interface{}); ok {
					if members, ok := m["members"].([]interface{}); ok {
						return output.PrintTable(os.Stdout, members, etcdMemberColumns)
					}
				}
				return output.PrintJSON(os.Stdout, parsed)
			})
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "HCP namespace (required)")
	_ = cmd.MarkFlagRequired("namespace")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Maximum time to wait")

	return cmd
}

func newEtcdDefragCmd() *cobra.Command {
	var (
		namespace string
		timeout   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "defrag",
		Short: "Defragment etcd storage",
		Long: `Run etcd defragmentation on all cluster members.
This is a mutating operation that compacts etcd storage.

Examples:
  gcphcp ops etcd defrag -n clusters-abc123`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEtcdCommand(cmd, "etcd-defrag", namespace, timeout, func(format output.Format, result map[string]interface{}) error {
				if format == output.FormatJSON {
					return output.PrintJSON(os.Stdout, result)
				}
				// defrag output is plain text
				if raw, ok := result["output"].(string); ok {
					fmt.Fprintln(os.Stdout, raw)
				} else {
					return output.PrintJSON(os.Stdout, result)
				}
				return nil
			})
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "HCP namespace (required)")
	_ = cmd.MarkFlagRequired("namespace")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Maximum time to wait")

	return cmd
}

func newEtcdCompactCmd() *cobra.Command {
	var (
		namespace string
		timeout   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Compact etcd revisions",
		Long: `Compact etcd revisions on each member individually.
Each member's current revision is discovered and compacted. This triggers
the sidecar auto-defrag mechanism.

Examples:
  gcphcp ops etcd compact -n clusters-abc123`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEtcdCommand(cmd, "etcd-compact", namespace, timeout, func(format output.Format, result map[string]interface{}) error {
				if format == output.FormatJSON {
					return output.PrintJSON(os.Stdout, result)
				}
				// compact returns "results" (string per member), not "output"
				results, _ := result["results"].([]interface{})
				for _, r := range results {
					if s, ok := r.(string); ok {
						fmt.Fprintln(os.Stdout, s)
					}
				}
				return nil
			})
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "HCP namespace (required)")
	_ = cmd.MarkFlagRequired("namespace")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "Maximum time to wait")

	return cmd
}

// runEtcdCommand is the shared workflow execution logic for all etcd subcommands.
func runEtcdCommand(cmd *cobra.Command, etcdCommand, namespace string, timeout time.Duration, printer func(output.Format, map[string]interface{}) error) error {
	project, _ := cmd.Flags().GetString("project")
	region, _ := cmd.Flags().GetString("region")
	outputFormat, _ := cmd.Flags().GetString("output")

	if project == "" {
		return fmt.Errorf("--project is required (or set GCPHCP_PROJECT)")
	}
	if region == "" {
		return fmt.Errorf("--region is required (or set GCPHCP_REGION)")
	}

	data := map[string]interface{}{
		"namespace": namespace,
		"command":   etcdCommand,
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	client, err := workflows.NewClient(ctx, project, region)
	if err != nil {
		return fmt.Errorf("creating client: %w", err)
	}
	defer client.Close()

	if err := checkPAMGate(ctx, client, "etcd-ops", cmd, os.Stderr); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Running %s (ns: %s)\n", etcdCommand, namespace)

	_, result, err := client.Run(ctx, "etcd-ops", data)
	if err != nil {
		return fmt.Errorf("executing workflow: %w", err)
	}

	if result.State == "FAILED" {
		// Some etcd commands (e.g. health) embed valid JSON in the error
		// when the job exits non-zero. Try to extract and display it.
		if parsed := parseJSONFromError(result.Error); parsed != nil {
			format := output.ParseFormat(outputFormat)
			if err := printer(format, map[string]interface{}{"output": parsed}); err != nil {
				return err
			}
			return fmt.Errorf("etcd reported errors (see output above)")
		}
		return fmt.Errorf("etcd-ops failed: %s", cleanEtcdError(result.Error))
	}

	format := output.ParseFormat(outputFormat)
	return printer(format, result.Result)
}

// cleanEtcdError extracts human-readable messages from a workflow RuntimeError.
// Filters out JSON log lines and workflow metadata, keeping only actionable output.
func cleanEtcdError(errMsg string) string {
	unescaped := strings.NewReplacer(`\"`, `"`, `\n`, "\n").Replace(errMsg)

	// Remove trailing workflow step info (e.g. 'in step "raise_error_result"...')
	if idx := strings.Index(unescaped, "\nin step "); idx != -1 {
		unescaped = unescaped[:idx]
	}

	// Strip RuntimeError wrapper
	if idx := strings.Index(unescaped, "RuntimeError: "); idx != -1 {
		unescaped = unescaped[idx+len("RuntimeError: "):]
	}
	unescaped = strings.Trim(unescaped, "\" \n")

	// Filter out JSON log lines, keep human-readable lines
	var lines []string
	for _, line := range strings.Split(unescaped, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "{") {
			continue
		}
		lines = append(lines, line)
	}

	if len(lines) > 0 {
		return strings.Join(lines, "\n")
	}

	return unescaped
}

// parseJSONFromError tries to find and parse a JSON array embedded in an error
// string. The workflow wraps the error in RuntimeError: "..." with escaped
// quotes, so we first unescape, then scan for JSON arrays.
func parseJSONFromError(errMsg string) interface{} {
	// Unescape: the workflow wraps output in a quoted string with backslash escapes
	unescaped := strings.NewReplacer(`\"`, `"`, `\\n`, "\n", `\n`, "\n").Replace(errMsg)

	// Scan for each '[' and try to parse a JSON array starting there
	for i := 0; i < len(unescaped); i++ {
		if unescaped[i] != '[' {
			continue
		}
		var arr []interface{}
		if err := json.Unmarshal([]byte(unescaped[i:]), &arr); err == nil {
			return arr
		}
		// Try to find the matching ']' for a partial parse
		depth := 0
		for j := i; j < len(unescaped); j++ {
			if unescaped[j] == '[' {
				depth++
			} else if unescaped[j] == ']' {
				depth--
				if depth == 0 {
					if err := json.Unmarshal([]byte(unescaped[i:j+1]), &arr); err == nil {
						return arr
					}
					break
				}
			}
		}
	}

	return nil
}

// --- Column definitions for etcd subcommands ---

var etcdHealthColumns = []output.Column{
	{Header: "ENDPOINT", Path: "endpoint", Transform: output.TransformShortenEndpoint},
	{Header: "HEALTH", Path: "health", Transform: output.TransformBool},
	{Header: "TOOK", Path: "took"},
	{Header: "ERROR", Path: "error", OmitEmpty: true},
}

var etcdStatusColumns = []output.Column{
	{Header: "ENDPOINT", Path: "Endpoint", Transform: output.TransformShortenEndpoint},
	{Header: "ROLE", Compute: func(item map[string]interface{}, allItems []interface{}) string {
		// Determine leader from the first item's Status.leader field
		var leaderID float64
		for _, it := range allItems {
			status := output.AsMap(output.AsMap(it)["Status"])
			if l, ok := status["leader"].(float64); ok {
				leaderID = l
				break
			}
		}
		header := output.AsMap(output.AsMap(item["Status"])["header"])
		if memberID, ok := header["member_id"].(float64); ok && memberID == leaderID {
			return "leader"
		}
		return "follower"
	}},
	{Header: "VERSION", Path: "Status.version"},
	{Header: "DB SIZE", Path: "Status.dbSize", Transform: output.TransformBytes},
	{Header: "DB IN USE", Path: "Status.dbSizeInUse", Transform: output.TransformBytes},
	{Header: "REVISION", Path: "Status.header.revision", Transform: output.TransformUint64},
	{Header: "RAFT INDEX", Path: "Status.raftIndex", Transform: output.TransformUint64},
	{Header: "RAFT TERM", Path: "Status.raftTerm", Transform: output.TransformUint64},
}

var etcdMemberColumns = []output.Column{
	{Header: "NAME", Path: "name"},
	{Header: "ID", Path: "ID", Transform: output.TransformUint64},
	{Header: "IS LEARNER", Path: "isLearner", Transform: output.TransformBool},
	{Header: "PEER URLS", Path: "peerURLs", Transform: output.TransformShortenURLList},
	{Header: "CLIENT URLS", Path: "clientURLs", Transform: output.TransformShortenURLList},
}

// parseEtcdOutput extracts and parses the "output" field from a workflow result.
// The output may be a raw JSON string (from pod logs) or already-parsed data.
func parseEtcdOutput(result map[string]interface{}) interface{} {
	raw, ok := result["output"]
	if !ok {
		return result
	}

	s, ok := raw.(string)
	if !ok {
		return raw
	}

	// Try parsing as JSON array
	var arr []interface{}
	if err := json.Unmarshal([]byte(s), &arr); err == nil {
		return arr
	}

	// Try parsing as JSON object
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(s), &obj); err == nil {
		return obj
	}

	return raw
}
