package iam

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/auth"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/discovery"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam/application"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	"github.com/spf13/cobra"
)

// newUpdateCommand keeps IAM updates distinct from release requests. Mutation
// uses a separately protected sidecar and never changes the original journal.
func newUpdateCommand(effective *config.Config) *cobra.Command {
	var version, group, installationState string
	var expected string
	var dryRun, confirm, resume, abandon bool
	cmd := &cobra.Command{
		Use:   "update <cluster-name>",
		Short: "Prepare retained IAM permissions for an existing Cluster",
		Long: `Preview with --dry-run. Applying only adds custom-role permissions and
records exact target coverage; it never requests an upgrade. Requires --confirm,
--expect-plan and the protected original --installation-state. Update intent is
saved beside that journal as .updates.json. Unknown results require --resume.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun == confirm || dryRun && resume || !dryRun && expected == "" {
				return fmt.Errorf("choose --dry-run or --confirm with --expect-plan; unfinished writes require --resume")
			}
			if abandon && (!resume || dryRun) {
				return fmt.Errorf("--abandon-unstarted requires --confirm --resume and the original --expect-plan")
			}
			if version == "" {
				return fmt.Errorf("an exact --version is required")
			}
			if group == "" {
				return fmt.Errorf("--channel-group is required")
			}
			if installationState == "" {
				return fmt.Errorf("--installation-state is required")
			}
			journal, err := application.Load(installationState)
			if err != nil {
				return err
			}
			project, _ := cmd.Flags().GetString("project")
			if journal.Plan.Identity.Project != project || journal.Cluster.Name != args[0] {
				return fmt.Errorf("installation journal does not match Cluster/project")
			}
			info, err := discovery.NewResolver().Region(cmd.Context(), effective)
			if err != nil {
				return err
			}
			if info.PlatformAPIEndpoint == "" {
				return discovery.EndpointRequiredError(discovery.KindAPI, effective.Environment)
			}
			client, err := platformapi.NewClient(info.PlatformAPIEndpoint, project, auth.NewTokenSource(auth.PlatformAPIAudience))
			if err != nil {
				return err
			}
			cloud, err := application.NewGCP(cmd.Context(), project)
			if err != nil {
				return err
			}
			if dryRun {
				updates, err := application.LoadUpdates(installationState, journal)
				if err != nil {
					return err
				}
				preview, err := application.PreviewLinkedUpdate(cmd.Context(), client, cloud, journal, updates, args[0], version, group, time.Now().UTC())
				if err != nil {
					return err
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(preview)
			}
			file, err := application.OpenUpdates(installationState)
			if err != nil {
				return err
			}
			defer file.Close()
			journal = file.Primary.Journal
			if resume {
				if len(file.State.Operations) == 0 {
					return fmt.Errorf("no linked update exists to resume")
				}
				op := file.State.Operations[len(file.State.Operations)-1]
				if op.Intent.PlanFingerprint != expected || op.Intent.Retention.Target.Version != version || op.Intent.Retention.Target.ChannelGroup != group {
					return fmt.Errorf("resume must name the exact journaled target and fingerprint")
				}
				if abandon {
					if err := application.AbandonUnstartedUpdate(journal, file.State); err != nil {
						return err
					}
					if err := file.SaveUpdate(file.State); err != nil {
						return err
					}
					return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"operationID": op.ID, "abandoned": true, "cloudMutated": false})
				}
			} else {
				preview, err := application.PreviewLinkedUpdate(cmd.Context(), client, cloud, journal, file.State, args[0], version, group, time.Now().UTC())
				if err != nil {
					return err
				}
				if err := application.PrepareUpdate(cmd.Context(), cloud, journal, file.State, preview, expected); err != nil {
					return err
				}
			}
			engine := &application.UpdateEngine{Client: client, Cloud: cloud, Writer: cloud, Store: file}
			if err := engine.Apply(cmd.Context(), journal, file.State); err != nil {
				op := file.State.Operations[len(file.State.Operations)-1]
				return fmt.Errorf("update %s incomplete; retain sidecar and use --resume with its original --expect-plan: %w", op.ID, err)
			}
			op := file.State.Operations[len(file.State.Operations)-1]
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"updateComplete": op.Phase == "Recorded", "operationID": op.ID, "templateRef": op.Intent.Retention.Target.Reference, "upgradeRequested": false})
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "Exact target release to inspect")
	cmd.Flags().StringVar(&group, "channel-group", "", "Explicit target channel group")
	cmd.Flags().StringVar(&installationState, "installation-state", "", "Protected original owned installation journal (read-only)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Read-only preview; never changes IAM or API state")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Apply only the exact previewed permission additions and IAM assertion")
	cmd.Flags().BoolVar(&resume, "resume", false, "Resume the linked operation after independent outcome readback")
	cmd.Flags().BoolVar(&abandon, "abandon-unstarted", false, "Record a tombstone only if no role write was ever attempted; requires --resume")
	cmd.Flags().StringVar(&expected, "expect-plan", "", "Exact reviewed preview fingerprint (original fingerprint on resume)")
	return cmd
}
