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

			allClusters := make([]hyperfleet.Cluster, 0)
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

				items := resp.JSON200.Items
				allClusters = append(allClusters, items...)

				if len(items) == 0 || int32(len(allClusters)) >= resp.JSON200.Total {
					break
				}
				page++
			}

			allClusters = deduplicateByName(allClusters)

			switch output.ParseFormat(outputFmt) {
			case output.FormatJSON:
				return output.PrintJSON(out, allClusters)
			case output.FormatYAML:
				return output.PrintYAML(out, allClusters)
			default:
			}

			if len(allClusters) == 0 {
				fmt.Fprintln(out, "No clusters found.")
				return nil
			}

			t := output.NewTable(out, "NAME", "REGION", "VERSION", "STATUS", "AGE")
			for _, c := range allClusters {
				t.AddRow(
					c.Name,
					c.Spec.Platform.Gcp.Region,
					releaseVersion(&c),
					clusterStatus(&c),
					output.Age(c.CreatedTime.Format("2006-01-02T15:04:05Z")),
				)
			}
			return t.Flush()
		},
	}

	cmd.Flags().StringVarP(&outputFmt, "output", "o", "text", "Output format: text, json, yaml")
	return cmd
}

// deduplicateByName keeps only the newest cluster for each name.
func deduplicateByName(clusters []hyperfleet.Cluster) []hyperfleet.Cluster {
	newest := make(map[string]hyperfleet.Cluster, len(clusters))
	for _, c := range clusters {
		if existing, ok := newest[c.Name]; !ok || c.CreatedTime.After(existing.CreatedTime) {
			newest[c.Name] = c
		}
	}
	result := make([]hyperfleet.Cluster, 0, len(newest))
	for _, c := range clusters {
		if n, ok := newest[c.Name]; ok && ptrStr(n.Id) == ptrStr(c.Id) {
			result = append(result, c)
			delete(newest, c.Name)
		}
	}
	return result
}
