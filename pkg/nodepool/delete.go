package nodepool

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDeleteCmd() *cobra.Command {
	var confirm bool

	cmd := &cobra.Command{
		Use:   "delete <nodepool-id-or-name>",
		Short: "Delete a nodepool",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("nodepool ID or name is required\n\nUsage: %s", cmd.UseLine())
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				return fmt.Errorf("--confirm is required to delete a nodepool")
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

			resp, err := client.DeleteNodePoolByIdWithResponse(ctx, clusterID, nodepoolID)
			if err != nil {
				return fmt.Errorf("deleting nodepool %s: %w", np.Name, err)
			}
			if resp.JSON202 == nil {
				if resp.HTTPResponse == nil {
					return fmt.Errorf("deleting nodepool %s: no response received", np.Name)
				}
				return fmt.Errorf("deleting nodepool %s: %s", np.Name, formatError(resp.HTTPResponse, resp.Body))
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Nodepool %s deletion initiated.\n", np.Name)
			return nil
		},
	}

	cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm deletion (required)")
	return cmd
}
