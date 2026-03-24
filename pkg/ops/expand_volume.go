package ops

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ckandag/gcp-hcp-cli/pkg/gcp/workflows"
	"github.com/ckandag/gcp-hcp-cli/pkg/output"
	"github.com/spf13/cobra"
)

func newExpandVolumeCmd() *cobra.Command {
	var (
		namespace string
		size      string
		timeout   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "expand-volume <pvc-name>",
		Short: "Expand a PersistentVolumeClaim",
		Long: `Expand a PVC's storage size via the expand-volume workflow.
The underlying StorageClass must have allowVolumeExpansion: true.

For etcd disk pressure, ensure the new size provides at least 2x the
DB size in free space for defragmentation.

Examples:
  # Expand etcd PVC to 20Gi
  gcphcp ops expand-volume data-etcd-0 -n clusters-abc123 --size 20Gi

  # Expand to 50Gi
  gcphcp ops expand-volume data-etcd-1 -n clusters-abc123 --size 50Gi`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pvcName := args[0]

			project, _ := cmd.Flags().GetString("project")
			region, _ := cmd.Flags().GetString("region")
			outputFormat, _ := cmd.Flags().GetString("output")

			if project == "" {
				return fmt.Errorf("--project is required (or set GCPHCP_PROJECT)")
			}
			if region == "" {
				return fmt.Errorf("--region is required (or set GCPHCP_REGION)")
			}

			data := map[string]interface{}{
				"namespace": namespace,
				"pvc_name":  pvcName,
				"new_size":  size,
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			client, err := workflows.NewClient(ctx, project, region)
			if err != nil {
				return fmt.Errorf("creating client: %w", err)
			}
			defer client.Close()

			if err := checkPAMGate(ctx, client, "expand-volume", cmd, os.Stderr); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Expanding PVC %s to %s (ns: %s)\n", pvcName, size, namespace)

			_, result, err := client.Run(ctx, "expand-volume", data)
			if err != nil {
				return fmt.Errorf("executing workflow: %w", err)
			}

			format := output.ParseFormat(outputFormat)
			if format == output.FormatJSON {
				return output.PrintJSON(os.Stdout, result.Result)
			}

			status := output.GetString(result.Result, "status")
			if status == "error" {
				errMsg := output.GetString(result.Result, "error")
				hint := output.GetString(result.Result, "hint")
				msg := fmt.Sprintf("failed to expand %s: %s", pvcName, errMsg)
				if hint != "" {
					msg += "\nhint: " + hint
				}
				return fmt.Errorf("%s", msg)
			}

			oldSize := output.GetString(result.Result, "old_size")
			newSize := output.GetString(result.Result, "new_size")
			fmt.Fprintf(os.Stdout, "persistentvolumeclaim \"%s\" expanded: %s → %s\n", pvcName, oldSize, newSize)
			return nil
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Kubernetes namespace (required)")
	_ = cmd.MarkFlagRequired("namespace")
	cmd.Flags().StringVar(&size, "size", "", "New storage size (e.g., 20Gi) (required)")
	_ = cmd.MarkFlagRequired("size")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Maximum time to wait")

	return cmd
}
