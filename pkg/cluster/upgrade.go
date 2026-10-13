package cluster

import (
	"fmt"
	"io"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

func newUpgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Request a cluster upgrade",
	}
	cmd.AddCommand(newUpgradeControlPlaneCmd())
	return cmd
}

func newUpgradeControlPlaneCmd() *cobra.Command {
	var version, outputFormat string
	cmd := &cobra.Command{
		Use:   "control-plane <cluster-name>",
		Short: "Request a one-time manual control-plane upgrade",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := clientFromCmd(cmd)
			request := newControlPlaneUpgradeRequest(client.Namespace(), args[0], version)
			created, err := client.ControlPlaneUpgradeRequests().Create(cmd.Context(), client.Namespace(), args[0], request)
			if err != nil {
				return fmt.Errorf("requesting control-plane upgrade: %w", err)
			}
			return printControlPlaneUpgradeRequest(cmd.OutOrStdout(), created, outputFormat)
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "Target version advertised by the cluster (required)")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "text", "Output format: text, json, yaml")
	_ = cmd.MarkFlagRequired("version")
	return cmd
}

func newControlPlaneUpgradeRequest(namespace, clusterName, version string) *platformapi.ControlPlaneUpgradeRequest {
	return &platformapi.ControlPlaneUpgradeRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: gcpv1.GroupVersion.String(),
			Kind:       "ControlPlaneUpgradeRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "manual-upgrade-",
			Namespace:    namespace,
		},
		Spec: platformapi.ControlPlaneUpgradeRequestSpec{
			ClusterID:     clusterName,
			TargetVersion: version,
		},
	}
}

func printControlPlaneUpgradeRequest(w io.Writer, request *platformapi.ControlPlaneUpgradeRequest, format string) error {
	switch output.ParseFormat(format) {
	case output.FormatJSON:
		return output.PrintJSON(w, request)
	case output.FormatYAML:
		encoded, err := yaml.Marshal(request)
		if err != nil {
			return err
		}
		_, err = w.Write(encoded)
		return err
	default:
		_, err := fmt.Fprintf(w, "Control-plane upgrade request %s created for cluster %s (target %s).\n", request.Name, request.Spec.ClusterID, request.Spec.TargetVersion)
		return err
	}
}
