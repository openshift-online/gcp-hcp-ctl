package cluster

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/spf13/cobra"
	utilversion "k8s.io/apimachinery/pkg/util/version"
	"sigs.k8s.io/yaml"
)

func newUpgradeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "upgrade", Short: "Request a cluster upgrade"}
	cmd.AddCommand(newUpgradeControlPlaneCmd())
	return cmd
}

func newUpgradeControlPlaneCmd() *cobra.Command {
	var version, outputFormat string
	cmd := &cobra.Command{
		Use: "control-plane <cluster-name>", Short: "Request a control-plane upgrade through the Cluster API",
		Long: `Set the Cluster's desired release. A major.minor value selects the latest
eligible patch in the selected channel; an exact semantic version is also accepted.
The target may wait for health or rollout capacity before the server approves it.
Use cluster get to see the observed Version, pending Target Version, and conditions.`,
		Args: cobra.ExactArgs(1),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(version) {
				return nil
			}
			if _, err := utilversion.ParseSemantic(version); err != nil || strings.HasPrefix(version, "v") || strings.Contains(version, "+") {
				return fmt.Errorf("--version must be major.minor or a complete semantic version without a leading v")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := json.Marshal(map[string]any{"spec": map[string]any{"release": map[string]string{"version": version}}})
			if err != nil {
				return fmt.Errorf("building upgrade patch: %w", err)
			}
			client := clientFromCmd(cmd)
			changed, err := client.Clusters().Patch(cmd.Context(), client.Namespace(), args[0], body)
			if err != nil {
				return fmt.Errorf("requesting control-plane upgrade: %w", err)
			}
			return printControlPlaneUpgrade(cmd.OutOrStdout(), changed, outputFormat)
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "Desired major.minor or exact version (required)")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "text", "Output format: text, json, yaml")
	_ = cmd.MarkFlagRequired("version")
	return cmd
}

func printControlPlaneUpgrade(w io.Writer, cluster *gcpv1.Cluster, format string) error {
	switch output.ParseFormat(format) {
	case output.FormatJSON:
		return output.PrintJSON(w, cluster)
	case output.FormatYAML:
		encoded, err := yaml.Marshal(cluster)
		if err != nil {
			return err
		}
		_, err = w.Write(encoded)
		return err
	default:
		_, err := fmt.Fprintf(w, "Control-plane upgrade requested for %s (target %s).\n", cluster.Name, cluster.Spec.Release.Version)
		return err
	}
}
