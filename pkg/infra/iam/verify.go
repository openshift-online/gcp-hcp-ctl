package iam

import (
	"encoding/json"
	"fmt"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/auth"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/discovery"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam/application"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	"github.com/spf13/cobra"
)

func newVerifyCommand(effective *config.Config) *cobra.Command {
	var stateFile string
	cmd := &cobra.Command{Use: "verify <infra-id>", Short: "Read back an owned IAM operation without changing cloud or API state", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		j, err := application.Load(stateFile)
		if err != nil {
			return err
		}
		project, _ := cmd.Flags().GetString("project")
		if j.Plan.Identity.InfraID != args[0] || j.Plan.Identity.Project != project {
			return fmt.Errorf("verification identity does not match journal")
		}
		info, err := discovery.NewResolver().Region(cmd.Context(), effective)
		if err != nil {
			return err
		}
		client, err := platformapi.NewClient(info.PlatformAPIEndpoint, project, auth.NewTokenSource(auth.PlatformAPIAudience))
		if err != nil {
			return err
		}
		if err := application.CheckRealm(cmd.Context(), client, &j.Plan); err != nil {
			return err
		}
		target, err := application.Resolve(cmd.Context(), client, j.Plan.Target.Version, j.Plan.Target.ChannelGroup)
		if err != nil {
			return err
		}
		if target.Reference != j.Plan.Target.Reference || target.VersionUID != j.Plan.Target.VersionUID || target.VersionGeneration != j.Plan.Target.VersionGeneration {
			return fmt.Errorf("verification catalog binding changed")
		}
		cloud, err := application.NewGCP(cmd.Context(), project)
		if err != nil {
			return err
		}
		updates, err := application.LoadUpdates(stateFile, j)
		if err != nil {
			return err
		}
		if updates != nil && updates.Cleanup != nil {
			return fmt.Errorf("installation cleanup has started; use destroy --dry-run to inspect progress")
		}
		if err := application.VerifyLinked(cmd.Context(), cloud, j, updates); err != nil {
			return err
		}
		complete := true
		phase := "NoUpdates"
		ref := target.Reference
		eligible := target.Eligible() == nil
		if updates != nil {
			for i := len(updates.Operations) - 1; i >= 0; i-- {
				op := updates.Operations[i]
				if op.Phase == "Abandoned" {
					continue
				}
				phase = op.Phase
				complete = op.Phase == "Recorded"
				ref = op.Intent.Retention.Target.Reference
				current, err := application.Resolve(cmd.Context(), client, op.Intent.Retention.Target.Version, op.Intent.Retention.Target.ChannelGroup)
				eligible = err == nil && current.Reference == ref && current.Eligible() == nil
				break
			}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"cloudReadbackMatches": true, "updateComplete": complete, "updatePhase": phase, "eligibleForNewUse": eligible, "operationID": j.OperationID, "templateRef": ref})
	}}
	cmd.Flags().StringVar(&stateFile, "state-file", "", "Protected operation journal (required)")
	return cmd
}
