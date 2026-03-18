package ops

import (
	"context"
	"io"
	"os"

	"github.com/ckandag/gcp-hcp-cli/pkg/gcp/workflows"
	"github.com/ckandag/gcp-hcp-cli/pkg/ops/pam"
	"github.com/spf13/cobra"
)

// checkPAMGate checks if a workflow is PAM-gated and ensures the user has an active grant.
func checkPAMGate(ctx context.Context, wfClient *workflows.Client, workflowName string, cmd *cobra.Command, stderr io.Writer) error {
	pamEntitlement, _ := cmd.Flags().GetString("pam-entitlement")

	var labels map[string]string
	if wfDetail, err := wfClient.GetWorkflow(ctx, workflowName); err == nil {
		labels = wfDetail.Labels
	} else if pamEntitlement != "" {
		labels = map[string]string{}
	} else {
		// Can't get workflow metadata and no explicit entitlement; skip PAM check
		return nil
	}

	reason, _ := cmd.Flags().GetString("reason")

	return pam.EnsurePAMGrant(ctx, wfClient.Project, pamEntitlement, reason, labels, os.Stdin, stderr)
}
