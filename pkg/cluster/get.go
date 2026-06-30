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
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("cluster ID or name is required\n\nUsage: %s", cmd.UseLine())
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cluster, err := resolveCluster(cmd.Context(), clientFromCmd(cmd), args[0])
			if err != nil {
				return err
			}

			return printCluster(cmd.OutOrStdout(), cluster, outputFmt)
		},
	}

	cmd.Flags().StringVarP(&outputFmt, "output", "o", "text", "Output format: text, json")
	return cmd
}
