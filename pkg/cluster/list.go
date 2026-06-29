package cluster

import (
	"fmt"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/hyperfleet"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var outputFmt string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all clusters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := clientFromCmd(cmd)
			out := cmd.OutOrStdout()

			var allClusters []hyperfleet.Cluster
			var page int32 = 1
			var pageSize int32 = 100

			for {
				params := &hyperfleet.GetClustersParams{
					Page:     &page,
					PageSize: &pageSize,
				}
				resp, err := client.GetClustersWithResponse(cmd.Context(), params)
				if err != nil {
					return fmt.Errorf("listing clusters: %w", err)
				}
				if resp.JSON200 == nil {
					return fmt.Errorf("listing clusters: %s", formatError(resp.HTTPResponse, resp.Body))
				}

				allClusters = append(allClusters, resp.JSON200.Items...)

				if int32(len(allClusters)) >= resp.JSON200.Total {
					break
				}
				page++
			}

			if outputFmt == "json" {
				return output.PrintJSON(out, allClusters)
			}

			if len(allClusters) == 0 {
				fmt.Fprintln(out, "No clusters found.")
				return nil
			}

			t := output.NewTable(out, "NAME", "ID", "PLATFORM", "REGION", "VERSION", "STATUS", "AGE")
			for _, c := range allClusters {
				t.AddRow(
					c.Name,
					truncateID(ptrStr(c.Id)),
					string(c.Spec.Platform.Type),
					c.Spec.Platform.Gcp.Region,
					releaseVersion(&c),
					clusterStatus(&c),
					output.Age(c.CreatedTime.Format("2006-01-02T15:04:05Z")),
				)
			}
			return t.Flush()
		},
	}

	cmd.Flags().StringVarP(&outputFmt, "output", "o", "text", "Output format: text, json")
	return cmd
}
