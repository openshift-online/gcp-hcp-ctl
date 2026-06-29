package cluster

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newGetCmd() *cobra.Command {
	var outputFmt string

	cmd := &cobra.Command{
		Use:   "get <cluster-id-or-name>",
		Short: "Get a cluster by ID or name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			apiEndpoint, _ := cmd.Flags().GetString("api-endpoint")
			client, err := newClient(apiEndpoint)
			if err != nil {
				return err
			}

			resp, err := client.GetClusterByIdWithResponse(cmd.Context(), args[0], nil)
			if err != nil {
				return fmt.Errorf("getting cluster %s: %w", args[0], err)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("getting cluster %s: %s", args[0], formatError(resp.HTTPResponse, resp.Body))
			}

			return printCluster(cmd.OutOrStdout(), resp.JSON200, outputFmt)
		},
	}

	cmd.Flags().StringVarP(&outputFmt, "output", "o", "text", "Output format: text, json")
	return cmd
}
