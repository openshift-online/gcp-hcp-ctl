package iam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/auth"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/discovery"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam/application"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/spf13/cobra"
)

type CreateOptions struct {
	StopAfterFirstRole  bool
	Version             string
	ChannelGroup        string
	DryRun              bool
	Confirm             bool
	Resume              bool
	StateFile           string
	ExpectPlan          string
	Catalog             application.Catalog
	Cloud               application.Cloud
	ProjectID           string
	InfraID             string
	ClusterOIDCJWKSFile string
	OutputFile          string
	OIDCIssuerURL       string
}

type CreateOutput struct {
	SchemaVersion        int                            `json:"schemaVersion,omitempty"`
	TemplateRef          *v1.WIFConfigTemplateReference `json:"templateRef,omitempty"`
	WorkerServiceAccount string                         `json:"workerServiceAccount,omitempty"`
	OperationID          string                         `json:"operationID,omitempty"`
	ProjectID            string                         `json:"projectId"`
	ProjectNumber        string                         `json:"projectNumber"`
	InfraID              string                         `json:"infraId"`
	WorkloadIdentityPool WorkloadIdentityConfig         `json:"workloadIdentityPool"`
	ServiceAccounts      map[string]string              `json:"serviceAccounts"`
}

type WorkloadIdentityConfig struct {
	PoolID     string `json:"poolId"`
	ProviderID string `json:"providerId"`
	Audience   string `json:"audience"`
}

// NewCreateCommand returns the "iam create" command. Unless the caller passes a
// complete --oidc-issuer-url or an --oidc-jwks-file, the OIDC issuer is derived
// from effective: discovered from --env/--region, or an explicit endpoint.
func NewCreateCommand(effective *config.Config) *cobra.Command {
	opts := &CreateOptions{ChannelGroup: "stable"}

	cmd := &cobra.Command{
		Use:   "create <infra-id>",
		Short: "Create GCP IAM infrastructure for a HyperShift cluster",
		Long: `Create Workload Identity Federation (WIF) infrastructure including:
  - Workload Identity Pool
  - OIDC Provider
  - 6 Google Service Accounts with IAM roles and WIF bindings

Uses the authenticated release catalog, owned operation state and fresh cloud
readback. Preview first with --dry-run; mutation requires --confirm,
--expect-plan and --state-file. Legacy name-only adoption is not supported.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			opts.InfraID = args[0]
			opts.ProjectID, _ = cmd.Flags().GetString("project")
			if opts.ClusterOIDCJWKSFile != "" {
				return fmt.Errorf("inline JWKS is not supported by the pinned installation flow")
			}
			if opts.Version == "" {
				return fmt.Errorf("an exact --version is required")
			}
			info, err := discovery.NewResolver().Region(cmd.Context(), effective)
			if err != nil {
				return err
			}
			if info.PlatformAPIEndpoint == "" {
				return discovery.EndpointRequiredError(discovery.KindAPI, effective.Environment)
			}
			expectedIssuer := strings.TrimRight(info.OIDCIssuer, "/") + "/" + opts.InfraID
			if opts.OIDCIssuerURL == "" {
				opts.OIDCIssuerURL = expectedIssuer
			}
			if info.OIDCIssuer == "" || opts.OIDCIssuerURL != expectedIssuer {
				return fmt.Errorf("issuer must match the selected region and installation identity")
			}
			client, err := platformapi.NewClient(info.PlatformAPIEndpoint, opts.ProjectID, auth.NewTokenSource(auth.PlatformAPIAudience))
			if err != nil {
				return err
			}
			opts.Catalog = client

			return opts.ValidateInputs()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.DryRun {
				p, cloud, err := opts.Prepare(cmd.Context())
				if err != nil {
					return err
				}
				var state *application.Journal
				if opts.Resume {
					state, err = application.Load(opts.StateFile)
					if err != nil {
						return err
					}
				}
				if err := (&application.Engine{Cloud: cloud}).Inspect(cmd.Context(), p, state); err != nil {
					return err
				}
				executable := p.Target.Eligible() == nil
				authorityBlock := ""
				if authority, ok := cloud.(interface{ CheckWriterAuthority(context.Context) error }); ok {
					if err := authority.CheckWriterAuthority(cmd.Context()); err != nil {
						executable = false
						authorityBlock = err.Error()
					}
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"executable": executable, "authorityBlock": authorityBlock, "planFingerprint": p.Fingerprint(), "plan": p, "stopAfterFirstRole": opts.StopAfterFirstRole})
			}
			logger := logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			result, err := opts.CreateIAM(cmd.Context(), logger)
			if err != nil {
				if errors.Is(err, application.ErrControlledStop) {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"paused": true, "iamVerified": false, "stoppedAfter": "first-unbound-role", "stateFile": opts.StateFile})
				}
				return err
			}
			if opts.OutputFile != "" {
				return opts.writeOutput(result, logger)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		},
	}

	cmd.Flags().StringVar(&opts.ClusterOIDCJWKSFile, "oidc-jwks-file", "", "Path to a local JSON file containing OIDC provider's public key in JWKS format")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", "", "Path to output JSON file with GSA details (default: stdout)")
	cmd.Flags().StringVar(&opts.OIDCIssuerURL, "oidc-issuer-url", "", "OIDC issuer URL for WIF provider (defaults to the discovered OIDC endpoint + /{infra-id})")
	cmd.Flags().StringVar(&opts.Version, "version", "", "Exact release whose retained IAM contract is required")
	cmd.Flags().StringVar(&opts.ChannelGroup, "channel-group", "stable", "Release channel group")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Read-only IAM plan; does not create state or cloud resources")
	cmd.Flags().BoolVar(&opts.Confirm, "confirm", false, "Confirm applying the exact previewed plan")
	cmd.Flags().BoolVar(&opts.Resume, "resume", false, "Resume the same journaled operation")
	cmd.Flags().StringVar(&opts.StateFile, "state-file", "", "Protected operation journal path")
	cmd.Flags().StringVar(&opts.ExpectPlan, "expect-plan", "", "Expected fresh preview fingerprint")
	cmd.Flags().BoolVar(&opts.StopAfterFirstRole, "stop-after-first-role", false, "Pause after one verified unbound custom role for owned cleanup rehearsal; emits no IAM success config")
	_ = cmd.Flags().MarkDeprecated("oidc-jwks-file", "inline JWKS is unsupported; use the selected regional issuer")

	return cmd
}

func (o *CreateOptions) ValidateInputs() error {
	if o.InfraID == "" {
		return fmt.Errorf("infra-id is required")
	}
	if o.ProjectID == "" {
		return fmt.Errorf("project is required")
	}
	if o.ClusterOIDCJWKSFile == "" && o.OIDCIssuerURL == "" {
		return fmt.Errorf("at least one of --oidc-jwks-file or --oidc-issuer-url is required")
	}

	if o.ClusterOIDCJWKSFile != "" {
		if err := o.ValidateJWKSFile(); err != nil {
			return fmt.Errorf("invalid JWKS file: %w", err)
		}
	}

	return nil
}

func (o *CreateOptions) Run(ctx context.Context, logger logr.Logger) error {
	results, err := o.CreateIAM(ctx, logger)
	if err != nil {
		return err
	}
	return o.writeOutput(results, logger)
}

func (o *CreateOptions) CreateIAM(ctx context.Context, logger logr.Logger) (*CreateOutput, error) {
	if !o.Confirm || o.DryRun {
		return nil, fmt.Errorf("IAM mutation requires --confirm and a matching --expect-plan")
	}
	p, cloud, err := o.Prepare(ctx)
	if err != nil {
		return nil, err
	}
	session, err := application.Start(ctx, o.Catalog, cloud, p, o.StateFile, o.ExpectPlan, o.Resume)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	session.Engine.StopAfterFirstRole = o.StopAfterFirstRole
	if err := session.Apply(ctx); err != nil {
		return nil, err
	}
	if err := session.Verify(ctx); err != nil {
		return nil, err
	}
	return Output(session), nil
}

func (o *CreateOptions) Prepare(ctx context.Context) (*application.Plan, application.Cloud, error) {
	if o.Version == "" || o.Catalog == nil {
		return nil, nil, fmt.Errorf("an authenticated catalog and exact --version are required; legacy IAM provisioning is disabled")
	}
	if o.ClusterOIDCJWKSFile != "" {
		return nil, nil, fmt.Errorf("inline JWKS is not supported by the pinned installation flow")
	}
	cloud := o.Cloud
	if cloud == nil {
		c, err := application.NewGCP(ctx, o.ProjectID)
		if err != nil {
			return nil, nil, err
		}
		cloud = c
	}
	p, err := application.Prepare(ctx, o.Catalog, cloud, o.ProjectID, o.InfraID, o.OIDCIssuerURL, o.Version, o.ChannelGroup)
	return p, cloud, err
}

func Output(s *application.Session) *CreateOutput {
	j := s.File.Journal
	p := &j.Plan
	ref := p.Target.Reference
	pool := p.Identity.InfraID + "-wi-pool"
	provider := p.Identity.InfraID + "-k8s-provider"
	return &CreateOutput{SchemaVersion: 1, ProjectID: p.Identity.Project, ProjectNumber: p.Identity.ProjectNumber, InfraID: p.Identity.InfraID, TemplateRef: &ref, WorkerServiceAccount: p.Identity.WorkerEmail, OperationID: j.OperationID, ServiceAccounts: p.Accounts(), WorkloadIdentityPool: WorkloadIdentityConfig{PoolID: pool, ProviderID: provider, Audience: "//iam.googleapis.com/projects/" + p.Identity.ProjectNumber + "/locations/global/workloadIdentityPools/" + pool + "/providers/" + provider}}
}

func (o *CreateOptions) writeOutput(results *CreateOutput, logger logr.Logger) error {
	out := os.Stdout
	if len(o.OutputFile) > 0 {
		var err error
		out, err = os.OpenFile(o.OutputFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("cannot create output file: %w", err)
		}
		defer func(out *os.File) {
			if err := out.Close(); err != nil {
				logger.Error(err, "Failed to close output file", "file", o.OutputFile)
			}
		}(out)
	}
	outputBytes, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize result: %w", err)
	}
	_, err = out.Write(outputBytes)
	if err != nil {
		return fmt.Errorf("failed to write result: %w", err)
	}
	return nil
}

func (o *CreateOptions) ValidateJWKSFile() error {
	jwksData, err := os.ReadFile(o.ClusterOIDCJWKSFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("cluster OIDC JWKS file does not exist: %s", o.ClusterOIDCJWKSFile)
		}
		return fmt.Errorf("failed to read JWKS file: %w", err)
	}

	var jwks map[string]interface{}
	if err := json.Unmarshal(jwksData, &jwks); err != nil {
		return fmt.Errorf("JWKS file contains invalid JSON: %w", err)
	}

	if _, exists := jwks["keys"]; !exists {
		return fmt.Errorf("JWKS file must contain a 'keys' field")
	}

	return nil
}
