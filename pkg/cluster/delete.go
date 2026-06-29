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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				return fmt.Errorf("--confirm is required to delete a cluster")
			}

			apiEndpoint, _ := cmd.Flags().GetString("api-endpoint")
			client, err := newClient(apiEndpoint)
			if err != nil {
				return err
			}

			resp, err := client.DeleteClusterByIdWithResponse(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("deleting cluster %s: %w", args[0], err)
			}
			if resp.HTTPResponse.StatusCode >= 400 {
				return fmt.Errorf("deleting cluster %s: %s", args[0], formatError(resp.HTTPResponse, resp.Body))
			}

			fmt.Printf("Cluster %s deletion initiated.\n", args[0])
			return nil
		},
	}

	cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm deletion (required)")
	return cmd
}
