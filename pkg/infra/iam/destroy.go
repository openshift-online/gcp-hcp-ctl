package iam

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/auth"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/discovery"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam/application"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/network"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	"github.com/spf13/cobra"
)

type DestroyOptions struct {
	IncludeOwnedNetwork bool
	StateFile           string
	DryRun              bool
	Cloud               application.Cloud
	Catalog             *platformapi.Client
	ProjectID           string
	InfraID             string
	SkipConfirm         bool
}

func NewDestroyCommand(effective ...*config.Config) *cobra.Command {
	opts := &DestroyOptions{}

	cmd := &cobra.Command{
		Use:   "destroy <infra-id>",
		Short: "Destroy GCP IAM infrastructure for a HyperShift cluster",
		Long: `Preview or remove only a journaled operation's proven-owned resources.

Requires the protected --state-file and independent normal hosted teardown proof
against the original API realm. Unknown submissions, active references, changed
identities and unreadable dependencies block cleanup. No Cluster is deleted here.

Use --dry-run first. --include-owned-network --yes additionally requests deletion
of only the recorded owned network. External networks and the default Compute
worker account are never deleted; unrelated policy members are preserved.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			opts.InfraID = args[0]
			opts.ProjectID, _ = cmd.Flags().GetString("project")
			if len(effective) > 0 {
				info, err := discovery.NewResolver().Region(cmd.Context(), effective[0])
				if err != nil {
					return err
				}
				client, err := platformapi.NewClient(info.PlatformAPIEndpoint, opts.ProjectID, auth.NewTokenSource(auth.PlatformAPIAudience))
				if err != nil {
					return err
				}
				opts.Catalog = client
			}
			return opts.ValidateInputs()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

			if opts.DryRun {
				j, err := application.Load(opts.StateFile)
				if err != nil {
					return err
				}
				if j.Plan.Identity.Project != opts.ProjectID || j.Plan.Identity.InfraID != opts.InfraID {
					return fmt.Errorf("cleanup identity does not match journal")
				}
				cloud := opts.Cloud
				if cloud == nil {
					cloud, err = application.NewGCP(cmd.Context(), opts.ProjectID)
					if err != nil {
						return err
					}
				}
				proof := opts.proof(cloud)
				updates, err := application.LoadUpdates(opts.StateFile, j)
				if err != nil {
					return err
				}
				engine, err := application.NewLinkedCleanupEngine(cloud, j, updates, nil)
				if err != nil {
					return err
				}
				checkJournal := j
				if updates != nil && updates.Cleanup != nil {
					checkJournal = updates.Cleanup
				}
				checkErr := engine.CheckCleanup(cmd.Context(), checkJournal, proof)
				if checkErr == nil && j.NetworkFingerprint != "" && !j.NetworkExternal && !opts.IncludeOwnedNetwork {
					checkErr = fmt.Errorf("owned network teardown requires explicit --include-owned-network")
				}
				block := ""
				if checkErr != nil {
					block = checkErr.Error()
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"operationID": j.OperationID, "phase": checkJournal.Phase, "cleanupEligible": checkErr == nil, "block": block, "includeOwnedNetwork": opts.IncludeOwnedNetwork, "resources": j.Plan.Resources, "grants": j.Plan.Grants})
			}
			if opts.IncludeOwnedNetwork && !opts.SkipConfirm {
				return fmt.Errorf("owned-network cleanup requires --yes after reviewing --dry-run")
			}
			if !opts.SkipConfirm {
				if !confirmDestroy(opts.InfraID, opts.ProjectID) {
					logger.Info("Destroy cancelled by user")
					return nil
				}
			}

			if err := opts.Run(cmd.Context(), logger); err != nil {
				logger.Error(err, "Failed to destroy GCP IAM infrastructure")
				return err
			}
			logger.Info("Successfully destroyed GCP IAM infrastructure")
			return nil
		},
	}

	cmd.Flags().BoolVar(&opts.SkipConfirm, "yes", false, "Skip confirmation prompt")
	cmd.Flags().StringVar(&opts.StateFile, "state-file", "", "Protected owned-operation journal (required)")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Show owned cleanup intent without cloud or API mutation")
	cmd.Flags().BoolVar(&opts.IncludeOwnedNetwork, "include-owned-network", false, "Also remove only the journaled owned network after normal hosted teardown")

	return cmd
}

func (o *DestroyOptions) ValidateInputs() error {
	if o.InfraID == "" {
		return fmt.Errorf("infra-id is required")
	}
	if o.ProjectID == "" {
		return fmt.Errorf("project is required")
	}
	return nil
}

func (o *DestroyOptions) Run(ctx context.Context, logger logr.Logger) error {
	return o.DestroyIAM(ctx, logger)
}

func (o *DestroyOptions) DestroyIAM(ctx context.Context, logger logr.Logger) error {
	j, err := application.Load(o.StateFile)
	if err != nil {
		return err
	}
	if j.Plan.Identity.Project != o.ProjectID || j.Plan.Identity.InfraID != o.InfraID {
		return fmt.Errorf("cleanup identity does not match the owned journal")
	}
	if o.Catalog == nil {
		return fmt.Errorf("authenticated platform teardown lookup is required")
	}
	cloud := o.Cloud
	if cloud == nil {
		cloud, err = application.NewGCP(ctx, o.ProjectID)
		if err != nil {
			return err
		}
	}
	linked, err := application.OpenUpdates(o.StateFile)
	if err != nil {
		return err
	}
	defer linked.Close()
	type cleanupState interface {
		CurrentJournal() *application.Journal
		Save(*application.Journal) error
	}
	var file cleanupState = linked.Primary
	var updates *application.UpdateJournal
	if len(linked.State.Operations) > 0 || linked.State.Cleanup != nil {
		file, updates = linked, linked.State
	}
	engine, err := application.NewLinkedCleanupEngine(cloud, linked.Primary.Journal, updates, file)
	if err != nil {
		return err
	}
	owned := file.CurrentJournal()
	proof := o.proof(cloud)
	if err := engine.CheckCleanup(ctx, owned, proof); err != nil {
		return err
	}
	if owned.NetworkFingerprint != "" && !owned.NetworkExternal {
		if !o.IncludeOwnedNetwork {
			return fmt.Errorf("journaled network teardown must be explicitly requested with --include-owned-network")
		}
		var plan network.OwnedPlan
		if err := json.Unmarshal(owned.NetworkSpec, &plan); err != nil {
			return fmt.Errorf("invalid network cleanup state")
		}
		nc, err := network.NewOwnedGCP(ctx, plan)
		if err != nil {
			return err
		}
		if err := network.CleanupOwned(ctx, nc, plan, file, proof); err != nil {
			return err
		}
	}
	return engine.Cleanup(ctx, owned, proof)
}

func (o *DestroyOptions) proof(cloud application.Cloud) func(context.Context, *application.Journal) error {
	return func(ctx context.Context, j *application.Journal) error {
		deps, ok := cloud.(interface {
			NoDependents(context.Context, *application.Plan) error
		})
		if !ok {
			return fmt.Errorf("independent cloud dependency inspection is required")
		}
		return (application.TeardownGuard{Client: o.Catalog, NoDependents: deps.NoDependents}).Check(ctx, j)
	}
}

func confirmDestroy(infraID, projectID string) bool {
	fmt.Fprintf(os.Stderr, "This will destroy all IAM resources for infra-id %q in project %q.\n", infraID, projectID)
	fmt.Fprintf(os.Stderr, "  - Only journaled owned component accounts, custom roles and managed grants\n")
	fmt.Fprintf(os.Stderr, "  - OIDC Provider\n")
	fmt.Fprintf(os.Stderr, "  - Workload Identity Pool\n")
	fmt.Fprintf(os.Stderr, "\nAre you sure? [y/N]: ")

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}

	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}
