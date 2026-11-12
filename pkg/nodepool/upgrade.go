package nodepool

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	"github.com/spf13/cobra"
	utilversion "k8s.io/apimachinery/pkg/util/version"
)

func newUpgradeCmd() *cobra.Command {
	var version, startTime, outputFmt string
	var clearStartTime bool
	cmd := &cobra.Command{
		Use:   "upgrade <nodepool-name>",
		Short: "Request an immediate or scheduled nodepool upgrade",
		Long: `Request an upgrade through the existing NodePool API.

The server validates the target before submitting it for installation.
Omit --start-time to preserve any existing schedule. If there is no schedule,
the upgrade is immediately eligible. Use --clear-start-time to clear a schedule.

  gcphcpctl nodepool upgrade workers --version 4.22.16
  gcphcpctl nodepool upgrade workers --version 4.22.16 --start-time 2026-11-20T03:00:00Z
  gcphcpctl nodepool upgrade workers --version 4.22.16 --clear-start-time`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return err
			}
			if version == "" {
				return fmt.Errorf("--version is required")
			}
			if _, err := utilversion.ParseSemantic(version); err != nil || strings.HasPrefix(version, "v") || strings.Contains(version, "+") {
				return fmt.Errorf("--version must be a full semantic version without a v prefix or build metadata")
			}
			if cmd.Flags().Changed("start-time") {
				if _, err := time.Parse(time.RFC3339, startTime); err != nil {
					return fmt.Errorf("--start-time must be an RFC 3339 timestamp")
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Preserve omitted release settings; explicit null clears a schedule.
			release := map[string]any{"version": version}
			if startTime != "" {
				timestamp, _ := time.Parse(time.RFC3339, startTime)
				release["upgradeStartTime"] = timestamp.UTC().Format(time.RFC3339)
			} else if clearStartTime {
				release["upgradeStartTime"] = nil
			}
			body, err := json.Marshal(map[string]any{"spec": map[string]any{"release": release}})
			if err != nil {
				return fmt.Errorf("building upgrade patch: %w", err)
			}
			client := clientFromCmd(cmd)
			updated, err := client.NodePools().Patch(cmd.Context(), client.Namespace(), args[0], body)
			if err != nil {
				return fmt.Errorf("requesting nodepool upgrade: %w", err)
			}
			if output.ParseFormat(outputFmt) != output.FormatText {
				return printNodePool(cmd.OutOrStdout(), updated, outputFmt)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Nodepool upgrade requested for %s (target %s).\n", args[0], version)
			return err
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "Target version (required)")
	cmd.Flags().StringVar(&startTime, "start-time", "", "Earliest upgrade start time (RFC 3339); omit to preserve the existing schedule")
	cmd.Flags().BoolVar(&clearStartTime, "clear-start-time", false, "Clear the existing upgrade start time (send explicit null)")
	cmd.MarkFlagsMutuallyExclusive("start-time", "clear-start-time")
	cmd.Flags().StringVarP(&outputFmt, "output", "o", "text", "Output format: text, json, yaml")
	return cmd
}
