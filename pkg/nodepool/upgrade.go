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
	cmd := &cobra.Command{
		Use:   "upgrade <nodepool-name>",
		Short: "Request an immediate or scheduled nodepool upgrade",
		Long: `Request an upgrade through the existing NodePool API.

The server validates the target before submitting it for installation.
Omit --start-time to request an immediate upgrade.

  gcphcpctl nodepool upgrade workers --version 4.22.16
  gcphcpctl nodepool upgrade workers --version 4.22.16 --start-time 2026-11-06T03:00:00Z`,
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
			if startTime != "" {
				if _, err := time.Parse(time.RFC3339, startTime); err != nil {
					return fmt.Errorf("--start-time must be an RFC 3339 timestamp")
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Explicit null clears an earlier pending schedule when an immediate
			// upgrade is requested. Release settings are customer-owned intent.
			release := map[string]any{"version": version, "upgradeStartTime": nil}
			if startTime != "" {
				timestamp, _ := time.Parse(time.RFC3339, startTime)
				release["upgradeStartTime"] = timestamp.UTC().Format(time.RFC3339)
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
	cmd.Flags().StringVar(&startTime, "start-time", "", "Earliest upgrade start time (RFC 3339); omit for immediate")
	cmd.Flags().StringVarP(&outputFmt, "output", "o", "text", "Output format: text, json, yaml")
	return cmd
}
