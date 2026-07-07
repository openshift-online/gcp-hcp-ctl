package nodepool

import (
	"context"
	"fmt"
	"os"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/hyperfleet"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var (
		outputFmt  string
		clusterRef string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List nodepools",
		Long: `List nodepools. Optionally filter by cluster with --cluster.

  gcphcpctl nodepool list
  gcphcpctl nodepool list --cluster my-cluster`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := clientFromCmd(cmd)
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			var clusterID string
			if clusterRef != "" {
				cluster, err := resolveCluster(ctx, client, clusterRef)
				if err != nil {
					return err
				}
				clusterID = ptrStr(cluster.Id)
				if clusterID == "" {
					return fmt.Errorf("cluster %q has no ID", clusterRef)
				}
			}

			allNodePools, err := fetchNodePools(ctx, client, clusterID)
			if err != nil {
				return err
			}

			switch output.ParseFormat(outputFmt) {
			case output.FormatJSON:
				return output.PrintJSON(out, allNodePools)
			case output.FormatYAML:
				return output.PrintYAML(out, allNodePools)
			default:
			}

			if len(allNodePools) == 0 {
				fmt.Fprintln(out, "No nodepools found.")
				return nil
			}

			clusterNames := buildClusterNameMap(ctx, client, allNodePools)

			t := output.NewTable(out, "NAME", "CLUSTER", "REPLICAS", "INSTANCE", "VERSION", "STATUS", "AGE")
			for _, np := range allNodePools {
				clusterDisplay := clusterNames[ptrStr(np.OwnerReferences.Id)]
				if clusterDisplay == "" {
					clusterDisplay = truncateID(ptrStr(np.OwnerReferences.Id))
				}
				t.AddRow(
					np.Name,
					clusterDisplay,
					replicas(&np),
					ptrStr(np.Spec.Platform.Gcp.InstanceType),
					releaseVersion(&np),
					nodePoolStatus(&np),
					output.Age(np.CreatedTime.Format("2006-01-02T15:04:05Z")),
				)
			}
			return t.Flush()
		},
	}

	cmd.Flags().StringVarP(&outputFmt, "output", "o", "text", "Output format: text, json, yaml")
	cmd.Flags().StringVar(&clusterRef, "cluster", "", "Filter by cluster name or ID")
	return cmd
}

// buildClusterNameMap resolves unique cluster IDs from the nodepool list
// into a map of ID -> Name via individual lookups. Falls back gracefully
// if a lookup fails.
func buildClusterNameMap(ctx context.Context, client *hyperfleet.ClientWithResponses, nodepools []hyperfleet.NodePool) map[string]string {
	ids := make(map[string]bool)
	for _, np := range nodepools {
		if id := ptrStr(np.OwnerReferences.Id); id != "" {
			ids[id] = true
		}
	}

	names := make(map[string]string, len(ids))
	for id := range ids {
		resp, err := client.GetClusterByIdWithResponse(ctx, id, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not resolve cluster name for %s: %v\n", truncateID(id), err)
			continue
		}
		if resp.JSON200 != nil {
			names[id] = resp.JSON200.Name
		}
	}
	return names
}
