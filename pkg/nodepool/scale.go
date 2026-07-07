package nodepool

import (
	"fmt"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/hyperfleet"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	"github.com/spf13/cobra"
)

func newScaleCmd() *cobra.Command {
	var (
		replicaCount int
		outputFmt    string
	)

	cmd := &cobra.Command{
		Use:   "scale <nodepool-id-or-name>",
		Short: "Scale a nodepool's replica count",
		Long: `Scale the number of replicas for a nodepool.

  gcphcpctl nodepool scale my-nodepool --replicas 5
  gcphcpctl nodepool scale my-nodepool --replicas 0`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("nodepool ID or name is required\n\nUsage: %s", cmd.UseLine())
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("replicas") {
				return fmt.Errorf("--replicas is required")
			}
			if replicaCount < 0 {
				return fmt.Errorf("--replicas must be non-negative")
			}

			client := clientFromCmd(cmd)
			ctx := cmd.Context()

			np, clusterID, err := resolveNodePool(ctx, client, args[0])
			if err != nil {
				return err
			}

			nodepoolID := ptrStr(np.Id)
			if nodepoolID == "" {
				return fmt.Errorf("nodepool %q has no ID", args[0])
			}

			// Platform is included because NodePoolSpec.Platform is a required
			// (non-pointer) field — omitting it would serialize as zero-valued
			// and overwrite the existing platform config.
			patchReq := hyperfleet.PatchNodePoolByIdJSONRequestBody{
				Spec: &hyperfleet.NodePoolSpec{
					Replicas: intPtr(replicaCount),
					Platform: np.Spec.Platform,
				},
			}

			resp, err := client.PatchNodePoolByIdWithResponse(ctx, clusterID, nodepoolID, patchReq)
			if err != nil {
				return fmt.Errorf("scaling nodepool %s: %w", np.Name, err)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("scaling nodepool %s: %s", np.Name, formatError(resp.HTTPResponse, resp.Body))
			}

			if output.ParseFormat(outputFmt) != output.FormatText {
				return printNodePool(cmd.OutOrStdout(), resp.JSON200, outputFmt)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Nodepool %s scaled to %d replicas.\n", np.Name, replicaCount)
			return nil
		},
	}

	cmd.Flags().IntVar(&replicaCount, "replicas", 0, "Number of replicas (required)")
	cmd.Flags().StringVarP(&outputFmt, "output", "o", "text", "Output format: text, json, yaml")
	return cmd
}
