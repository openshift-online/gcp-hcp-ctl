package cluster

import (
	"fmt"
	"os"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var outputFmt string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all clusters",
		RunE: func(cmd *cobra.Command, args []string) error {
			apiEndpoint, _ := cmd.Flags().GetString("api-endpoint")
			client, err := newClient(apiEndpoint)
			if err != nil {
				return err
			}

			resp, err := client.GetClustersWithResponse(cmd.Context(), nil)
			if err != nil {
				return fmt.Errorf("listing clusters: %w", err)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("listing clusters: %s", formatError(resp.HTTPResponse, resp.Body))
			}
			list := resp.JSON200

			if outputFmt == "json" {
				return output.PrintJSON(os.Stdout, list)
			}

			if len(list.Items) == 0 {
				fmt.Println("No clusters found.")
				return nil
			}

			t := output.NewTable(os.Stdout, "NAME", "ID", "PLATFORM", "REGION", "VERSION", "STATUS", "AGE")
			for _, c := range list.Items {
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
