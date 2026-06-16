package iam

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

type DestroyOptions struct {
	ProjectID   string
	InfraID     string
	SkipConfirm bool
}

func NewDestroyCommand() *cobra.Command {
	opts := &DestroyOptions{}

	cmd := &cobra.Command{
		Use:   "destroy <infra-id>",
		Short: "Destroy GCP IAM infrastructure for a HyperShift cluster",
		Long: `Destroy all IAM resources created for a cluster in reverse order:
  1. Service Accounts (with role binding cleanup)
  2. OIDC Provider
  3. Workload Identity Pool

All delete operations tolerate not-found errors (idempotent).`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			opts.InfraID = args[0]
			opts.ProjectID, _ = cmd.Flags().GetString("project")
			return opts.ValidateInputs()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

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
	iamManager, err := NewManager(ctx, o.ProjectID, o.InfraID, "", logger)
	if err != nil {
		return fmt.Errorf("failed to initialize GCP clients: %w", err)
	}

	if err := iamManager.DeleteServiceAccounts(ctx); err != nil {
		return fmt.Errorf("failed to delete service accounts: %w", err)
	}

	if err := iamManager.DeleteOIDCProvider(ctx); err != nil {
		return fmt.Errorf("failed to delete OIDC provider: %w", err)
	}

	if err := iamManager.DeleteWorkloadIdentityPool(ctx); err != nil {
		return fmt.Errorf("failed to delete workload identity pool: %w", err)
	}
	return nil
}

func confirmDestroy(infraID, projectID string) bool {
	fmt.Fprintf(os.Stderr, "This will destroy all IAM resources for infra-id %q in project %q.\n", infraID, projectID)
	fmt.Fprintf(os.Stderr, "  - 6 Google Service Accounts and their role bindings\n")
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
