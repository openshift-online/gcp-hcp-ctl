package cluster

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDeleteCmd() *cobra.Command {
	var confirm bool

	cmd := &cobra.Command{
		Use:   "delete <cluster-id-or-name>",
		Short: "Delete a cluster",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("cluster ID or name is required\n\nUsage: %s", cmd.UseLine())
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				return fmt.Errorf("--confirm is required to delete a cluster")
			}

			client := clientFromCmd(cmd)
			cluster, err := resolveCluster(cmd.Context(), client, args[0])
			if err != nil {
				return err
			}

			clusterID := ptrStr(cluster.Id)
			if clusterID == "" {
				return fmt.Errorf("cluster %q has no ID", args[0])
			}

			resp, err := client.DeleteClusterByIdWithResponse(cmd.Context(), clusterID)
			if err != nil {
				return fmt.Errorf("deleting cluster %s: %w", cluster.Name, err)
			}
			if resp.JSON202 == nil {
				if resp.HTTPResponse == nil {
					return fmt.Errorf("deleting cluster %s: no response received", cluster.Name)
				}
				return fmt.Errorf("deleting cluster %s: %s", cluster.Name, formatError(resp.HTTPResponse, resp.Body))
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Cluster %s deletion initiated.\n", cluster.Name)
			return nil
		},
	}

	cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm deletion (required)")
	return cmd
}
