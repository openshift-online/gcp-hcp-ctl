package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/go-logr/logr"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/hyperfleet"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/network"
	"github.com/spf13/cobra"
)

const maxInfraIDLength = 15

var (
	sanitizePattern  = regexp.MustCompile(`[^a-z0-9-]`)
	validInfraIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

func validateInfraID(id string) error {
	if len(id) > maxInfraIDLength {
		return fmt.Errorf("infra ID %q exceeds maximum length of %d characters", id, maxInfraIDLength)
	}
	if !validInfraIDPattern.MatchString(id) {
		return fmt.Errorf("infra ID %q is invalid: must start with a lowercase letter and contain only lowercase letters, digits, or hyphens", id)
	}
	return nil
}

// generateCompliantInfraID creates a GCP-compliant infra ID from a cluster name.
// Compliance rules: must start with a lowercase letter, contain only lowercase
// letters, digits, or hyphens, and be at most 15 characters long.
// The name is sanitized and truncated, then a random 4-char hex suffix is
// appended for uniqueness. Result format: <prefix>-<4hex>, e.g. "mycluster-a3f1".
func generateCompliantInfraID(clusterName string) (string, error) {
	sanitized := strings.ToLower(clusterName)
	sanitized = sanitizePattern.ReplaceAllString(sanitized, "")
	sanitized = strings.TrimLeft(sanitized, "-0123456789")
	sanitized = strings.Trim(sanitized, "-")

	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random suffix: %w", err)
	}
	suffix := hex.EncodeToString(b)

	maxPrefix := maxInfraIDLength - len(suffix) - 1
	if len(sanitized) > maxPrefix {
		sanitized = strings.TrimRight(sanitized[:maxPrefix], "-")
	}

	if sanitized == "" {
		sanitized = "hc"
	}

	return sanitized + "-" + suffix, nil
}

type createOptions struct {
	iamConfigFile     string
	networkConfigFile string
	setupInfra        bool
	endpointAccess    string
	version           string
	channelGroup      string
	dryRun            bool
	outputFmt         string
}

func newCreateCmd() *cobra.Command {
	opts := &createOptions{}

	cmd := &cobra.Command{
		Use:   "create <cluster-name>",
		Short: "Create a cluster",
		Long: `Create a cluster via the HyperFleet API.

Two modes of operation:

  1. Config files: assemble from iam-config.json and network-config.json
     gcphcpctl cluster create my-cluster \
       --iam-config-file iam-config.json \
       --network-config-file network-config.json \
       --version 4.22.0-rc.5 --channel-group candidate

  2. Setup infra: provision IAM and network automatically, then create cluster
     gcphcpctl cluster create my-cluster \
       --setup-infra --project my-project \
       --version 4.22.0-rc.5 --channel-group candidate

Both --iam-config-file and --network-config-file are required in config-file mode.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("cluster name is required\n\nUsage: %s", cmd.UseLine())
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(cmd, args[0])
		},
	}

	cmd.Flags().StringVar(&opts.iamConfigFile, "iam-config-file", "", "Path to IAM config JSON from 'gcphcpctl iam create'")
	cmd.Flags().StringVar(&opts.networkConfigFile, "network-config-file", "", "Path to network config JSON from 'gcphcpctl network create'")
	cmd.Flags().BoolVar(&opts.setupInfra, "setup-infra", false, "Automatically provision IAM and network infrastructure before creating cluster")
	cmd.Flags().StringVar(&opts.endpointAccess, "endpoint-access", "PublicAndPrivate", "API server endpoint access: Private or PublicAndPrivate")
	cmd.Flags().StringVar(&opts.version, "version", "", "OCP version (e.g. 4.22.0-rc.5)")
	cmd.Flags().StringVar(&opts.channelGroup, "channel-group", "stable", "Channel group: stable, fast, candidate, eus")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Show payload without creating")
	cmd.Flags().StringVarP(&opts.outputFmt, "output", "o", "text", "Output format: text, json, yaml")

	return cmd
}

func (o *createOptions) run(cmd *cobra.Command, clusterName string) error {
	if o.dryRun && o.setupInfra {
		return fmt.Errorf("--dry-run cannot be combined with --setup-infra because setup-infra has side effects")
	}

	if !hyperfleet.GCPClusterPlatformEndpointAccess(o.endpointAccess).Valid() {
		return fmt.Errorf("--endpoint-access must be one of: Private, PublicAndPrivate")
	}
	switch o.channelGroup {
	case "", "stable", "fast", "candidate", "eus":
	default:
		return fmt.Errorf("--channel-group must be one of: stable, fast, candidate, eus")
	}

	oidcBase, _ := cmd.Flags().GetString("oidc-endpoint")
	if oidcBase == "" {
		return fmt.Errorf("--oidc-endpoint is required (or set GCPHCPCTL_OIDC_ENDPOINT or oidc_endpoint in config)")
	}

	infraID, err := generateCompliantInfraID(clusterName)
	if err != nil {
		return fmt.Errorf("generating infra ID: %w", err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Generated infra ID: %s\n", infraID)

	project, _ := cmd.Flags().GetString("project")
	region, _ := cmd.Flags().GetString("region")
	if region == "" {
		region = "us-central1"
	}

	client := clientFromCmd(cmd)

	opts := buildPayloadOptions{
		clusterName:    clusterName,
		infraID:        infraID,
		projectID:      project,
		region:         region,
		endpointAccess: o.endpointAccess,
		oidcEndpoint:   oidcBase,
		version:        o.version,
		channelGroup:   o.channelGroup,
	}

	var req *hyperfleet.ClusterCreateRequest

	switch {
	case o.iamConfigFile != "":
		if o.networkConfigFile == "" {
			return fmt.Errorf("--network-config-file is required with --iam-config-file")
		}
		req, err = buildPayloadFromConfigs(o.iamConfigFile, o.networkConfigFile, opts)
		if err != nil {
			return err
		}

	case o.setupInfra:
		req, err = buildPayloadWithInfraSetup(cmd.Context(), cmd.ErrOrStderr(), opts)
		if err != nil {
			return err
		}

	default:
		return fmt.Errorf("either --setup-infra or --iam-config-file and --network-config-file are required")
	}

	if o.dryRun {
		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "Dry run — would POST this payload:")
		data, err := json.MarshalIndent(req, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling payload: %w", err)
		}
		fmt.Fprintln(out, string(data))
		return nil
	}

	resp, err := client.PostClusterWithResponse(cmd.Context(), *req)
	if err != nil {
		return fmt.Errorf("creating cluster: %w", err)
	}
	if resp.JSON201 == nil {
		if resp.HTTPResponse == nil {
			return fmt.Errorf("creating cluster: no response received")
		}
		return fmt.Errorf("creating cluster: %s", formatError(resp.HTTPResponse, resp.Body))
	}

	return printCluster(cmd.OutOrStdout(), resp.JSON201, o.outputFmt)
}

type buildPayloadOptions struct {
	clusterName    string
	infraID        string
	projectID      string
	region         string
	endpointAccess string
	oidcEndpoint   string
	version        string
	channelGroup   string
}

func (o buildPayloadOptions) issuerURL() string {
	return fmt.Sprintf("%s/%s", strings.TrimRight(o.oidcEndpoint, "/"), o.infraID)
}

// buildPayloadFromConfigs assembles a cluster creation payload from
// pre-provisioned IAM and network config files (output of 'gcphcpctl iam create'
// and 'gcphcpctl network create').
func buildPayloadFromConfigs(iamConfigFile, networkConfigFile string, opts buildPayloadOptions) (*hyperfleet.ClusterCreateRequest, error) {
	iamData, err := os.ReadFile(iamConfigFile)
	if err != nil {
		return nil, fmt.Errorf("reading IAM config: %w", err)
	}
	var iamConfig iam.CreateOutput
	if err := json.Unmarshal(iamData, &iamConfig); err != nil {
		return nil, fmt.Errorf("parsing IAM config: %w", err)
	}

	if iamConfig.InfraID != "" {
		if err := validateInfraID(iamConfig.InfraID); err != nil {
			return nil, fmt.Errorf("IAM config: %w", err)
		}
		opts.infraID = iamConfig.InfraID
	}
	if opts.projectID == "" {
		opts.projectID = iamConfig.ProjectID
	}

	networkData, err := os.ReadFile(networkConfigFile)
	if err != nil {
		return nil, fmt.Errorf("reading network config: %w", err)
	}
	var netConfig network.CreateOutput
	if err := json.Unmarshal(networkData, &netConfig); err != nil {
		return nil, fmt.Errorf("parsing network config: %w", err)
	}
	if netConfig.ProjectID != "" && netConfig.ProjectID != opts.projectID {
		return nil, fmt.Errorf("network config projectId %q does not match IAM config projectId %q", netConfig.ProjectID, opts.projectID)
	}
	if netConfig.Region != "" {
		opts.region = netConfig.Region
	}

	return assemblePayload(&iamConfig, &netConfig, opts)
}

// buildPayloadWithInfraSetup provisions IAM and network infrastructure on the fly,
// then assembles a cluster creation payload from the provisioned resources.
func buildPayloadWithInfraSetup(ctx context.Context, w io.Writer, opts buildPayloadOptions) (*hyperfleet.ClusterCreateRequest, error) {
	if opts.projectID == "" {
		return nil, fmt.Errorf("--project is required with --setup-infra (or set GCPHCPCTL_PROJECT or project in config)")
	}
	logger := logr.FromSlogHandler(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))

	fmt.Fprintln(w, ">>> Step 1: Setup IAM infrastructure")
	iamOpts := &iam.CreateOptions{
		ProjectID:     opts.projectID,
		InfraID:       opts.infraID,
		OIDCIssuerURL: opts.issuerURL(),
	}
	iamOutput, err := iamOpts.CreateIAM(ctx, logger)
	if err != nil {
		return nil, fmt.Errorf("IAM setup failed: %w", err)
	}
	fmt.Fprintf(w, "  IAM setup complete: %d service accounts created\n", len(iamOutput.ServiceAccounts))

	fmt.Fprintln(w, ">>> Step 2: Setup network infrastructure")
	networkOpts := &network.CreateOptions{
		ProjectID: opts.projectID,
		Region:    opts.region,
		InfraID:   opts.infraID,
	}
	networkOutput, err := networkOpts.CreateNetwork(ctx, logger)
	if err != nil {
		return nil, fmt.Errorf("network setup failed: %w", err)
	}
	fmt.Fprintf(w, "  Network setup complete: VPC=%s, Subnet=%s\n", networkOutput.NetworkName, networkOutput.SubnetName)

	fmt.Fprintln(w, ">>> Step 3: Building cluster payload")
	return assemblePayload(iamOutput, networkOutput, opts)
}

func validatePayloadInputs(projectID, region string, iamOutput *iam.CreateOutput, netOutput *network.CreateOutput) error {
	if projectID == "" {
		return fmt.Errorf("IAM config missing required field: projectId")
	}
	if region == "" {
		return fmt.Errorf("missing required field: region")
	}
	if iamOutput.ProjectNumber == "" {
		return fmt.Errorf("IAM config missing required field: projectNumber")
	}
	if iamOutput.WorkloadIdentityPool.PoolID == "" {
		return fmt.Errorf("IAM config missing required field: workloadIdentityPool.poolId")
	}
	if iamOutput.WorkloadIdentityPool.ProviderID == "" {
		return fmt.Errorf("IAM config missing required field: workloadIdentityPool.providerId")
	}
	requiredSAs := []string{"ctrlplane-op", "nodepool-mgmt", "cloud-controller", "gcp-pd-csi", "image-registry", "cloud-network"}
	for _, sa := range requiredSAs {
		if iamOutput.ServiceAccounts[sa] == "" {
			return fmt.Errorf("IAM config missing required service account: %s", sa)
		}
	}
	if netOutput.NetworkName == "" {
		return fmt.Errorf("network config missing required field: networkName")
	}
	if netOutput.SubnetName == "" {
		return fmt.Errorf("network config missing required field: subnetName")
	}
	return nil
}

func assemblePayload(iamOutput *iam.CreateOutput, netOutput *network.CreateOutput, opts buildPayloadOptions) (*hyperfleet.ClusterCreateRequest, error) {
	if err := validatePayloadInputs(opts.projectID, opts.region, iamOutput, netOutput); err != nil {
		return nil, err
	}

	issuerURL := opts.issuerURL()

	ea := hyperfleet.GCPClusterPlatformEndpointAccess(opts.endpointAccess)

	saRef := &hyperfleet.GCPServiceAccountsRef{
		ControlPlaneEmail:    strPtr(iamOutput.ServiceAccounts["ctrlplane-op"]),
		NodePoolEmail:        strPtr(iamOutput.ServiceAccounts["nodepool-mgmt"]),
		CloudControllerEmail: strPtr(iamOutput.ServiceAccounts["cloud-controller"]),
		StorageEmail:         strPtr(iamOutput.ServiceAccounts["gcp-pd-csi"]),
		ImageRegistryEmail:   strPtr(iamOutput.ServiceAccounts["image-registry"]),
		NetworkEmail:         strPtr(iamOutput.ServiceAccounts["cloud-network"]),
	}

	wif := &hyperfleet.GCPWorkloadIdentity{
		ProjectNumber:      iamOutput.ProjectNumber,
		PoolID:             iamOutput.WorkloadIdentityPool.PoolID,
		ProviderID:         iamOutput.WorkloadIdentityPool.ProviderID,
		ServiceAccountsRef: saRef,
	}

	spec := hyperfleet.ClusterSpec{
		InfraID:   &opts.infraID,
		IssuerURL: &issuerURL,
		Platform: hyperfleet.ClusterPlatformSpec{
			Type: hyperfleet.ClusterPlatformSpecTypeGcp,
			Gcp: hyperfleet.GCPClusterPlatform{
				ProjectID:        opts.projectID,
				Region:           opts.region,
				Network:          &netOutput.NetworkName,
				Subnet:           &netOutput.SubnetName,
				EndpointAccess:   &ea,
				WorkloadIdentity: wif,
			},
		},
	}

	if opts.version != "" {
		spec.Release = &hyperfleet.ReleaseSpec{
			Version: &opts.version,
		}
		if opts.channelGroup != "" {
			spec.Release.ChannelGroup = &opts.channelGroup
		}
	}

	kind := "Cluster"
	// Default shard label for HyperFleet scheduling; all CLI-created clusters
	// are assigned to shard 1 until multi-shard support is exposed.
	labels := map[string]string{"shard": "1"}

	return &hyperfleet.ClusterCreateRequest{
		Name:   opts.clusterName,
		Spec:   spec,
		Kind:   &kind,
		Labels: &labels,
	}, nil
}
