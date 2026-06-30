package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/go-logr/logr"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/hyperfleet"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/network"
	"github.com/spf13/cobra"
)

const maxInfraIDLength = 15

var infraIDPattern = regexp.MustCompile(`^[a-z][-a-z0-9]*$`)

// validateInfraID checks GCP resource naming constraints: must start with a
// lowercase letter, contain only lowercase letters, digits, or hyphens, and
// be at most 15 characters.
func validateInfraID(infraID string) error {
	if len(infraID) > maxInfraIDLength {
		return fmt.Errorf("infrastructure ID %q is too long (%d chars, max %d)", infraID, len(infraID), maxInfraIDLength)
	}
	if !infraIDPattern.MatchString(infraID) {
		return fmt.Errorf("infrastructure ID %q is invalid: must start with a lowercase letter and contain only lowercase letters, digits, or hyphens", infraID)
	}
	return nil
}

type createOptions struct {
	iamConfigFile     string
	networkConfigFile string
	setupInfra        bool
	infraID           string
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
	cmd.Flags().StringVar(&opts.infraID, "infra-id", "", "Infrastructure ID (defaults to cluster name)")
	cmd.Flags().StringVar(&opts.endpointAccess, "endpoint-access", "PublicAndPrivate", "API server endpoint access: Private or PublicAndPrivate")
	cmd.Flags().StringVar(&opts.version, "version", "", "OCP version (e.g. 4.22.0-rc.5)")
	cmd.Flags().StringVar(&opts.channelGroup, "channel-group", "stable", "Channel group: stable, fast, candidate, eus")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Show payload without creating")
	cmd.Flags().StringVarP(&opts.outputFmt, "output", "o", "text", "Output format: text, json")

	return cmd
}

func (o *createOptions) run(cmd *cobra.Command, clusterName string) error {
	if o.dryRun && o.setupInfra {
		return fmt.Errorf("--dry-run cannot be combined with --setup-infra because setup-infra has side effects")
	}

	switch o.endpointAccess {
	case "Private", "PublicAndPrivate":
	default:
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

	client := clientFromCmd(cmd)

	payloadOpts := buildPayloadOptions{
		endpointAccess: o.endpointAccess,
		oidcEndpoint:   oidcBase,
		version:        o.version,
		channelGroup:   o.channelGroup,
	}

	var (
		payload []byte
		err     error
	)

	switch {
	case o.iamConfigFile != "":
		if o.networkConfigFile == "" {
			return fmt.Errorf("--network-config-file is required with --iam-config-file")
		}
		globalRegion, _ := cmd.Flags().GetString("region")
		payload, err = buildPayloadFromConfigs(clusterName, o.iamConfigFile, o.networkConfigFile, globalRegion, payloadOpts)
		if err != nil {
			return err
		}

	case o.setupInfra:
		project, _ := cmd.Flags().GetString("project")
		region, _ := cmd.Flags().GetString("region")
		payload, err = buildPayloadWithInfraSetup(cmd.Context(), cmd.ErrOrStderr(), clusterName, project, region, o.infraID, payloadOpts)
		if err != nil {
			return err
		}

	default:
		return fmt.Errorf("either --setup-infra or --iam-config-file and --network-config-file are required")
	}

	if o.dryRun {
		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "Dry run — would POST this payload:")
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, payload, "", "  "); err != nil {
			out.Write(payload)
		} else {
			pretty.WriteTo(out)
		}
		fmt.Fprintln(out)
		return nil
	}

	resp, err := client.PostClusterWithBodyWithResponse(cmd.Context(), "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating cluster: %w", err)
	}
	if resp.JSON201 == nil {
		return fmt.Errorf("creating cluster: %s", formatError(resp.HTTPResponse, resp.Body))
	}

	return printCluster(cmd.OutOrStdout(), resp.JSON201, o.outputFmt)
}

type buildPayloadOptions struct {
	endpointAccess string
	oidcEndpoint   string
	version        string
	channelGroup   string
}

// buildPayloadFromConfigs assembles a cluster creation payload from
// pre-provisioned IAM and network config files (output of 'gcphcpctl iam create'
// and 'gcphcpctl network create').
func buildPayloadFromConfigs(clusterName, iamConfigFile, networkConfigFile, defaultRegion string, opts buildPayloadOptions) ([]byte, error) {
	// Load IAM config — provides project, infra ID, WI pool, and service accounts.
	iamData, err := os.ReadFile(iamConfigFile)
	if err != nil {
		return nil, fmt.Errorf("reading IAM config: %w", err)
	}
	var iamConfig iam.CreateOutput
	if err := json.Unmarshal(iamData, &iamConfig); err != nil {
		return nil, fmt.Errorf("parsing IAM config: %w", err)
	}

	infraID := iamConfig.InfraID
	if infraID == "" {
		infraID = clusterName
	}
	if err := validateInfraID(infraID); err != nil {
		return nil, fmt.Errorf("infraID in your IAM config is not valid: %w", err)
	}
	projectID := iamConfig.ProjectID
	region := defaultRegion
	if region == "" {
		region = "us-central1"
	}

	// Load network config — provides VPC, subnet, and region.
	networkData, err := os.ReadFile(networkConfigFile)
	if err != nil {
		return nil, fmt.Errorf("reading network config: %w", err)
	}
	var netConfig network.CreateOutput
	if err := json.Unmarshal(networkData, &netConfig); err != nil {
		return nil, fmt.Errorf("parsing network config: %w", err)
	}
	if netConfig.ProjectID != "" && netConfig.ProjectID != projectID {
		return nil, fmt.Errorf("network config projectId %q does not match IAM config projectId %q", netConfig.ProjectID, projectID)
	}
	if netConfig.Region != "" {
		region = netConfig.Region
	}

	return assemblePayload(clusterName, infraID, projectID, region, &iamConfig, &netConfig, opts)
}

// buildPayloadWithInfraSetup provisions IAM and network infrastructure on the fly,
// then assembles a cluster creation payload from the provisioned resources.
func buildPayloadWithInfraSetup(ctx context.Context, w io.Writer, clusterName, projectID, region, infraID string, opts buildPayloadOptions) ([]byte, error) {
	if projectID == "" {
		return nil, fmt.Errorf("--project is required with --setup-infra (or set GCPHCPCTL_PROJECT or project in config)")
	}
	if region == "" {
		region = "us-central1"
	}
	if infraID == "" {
		infraID = clusterName
	}
	if err := validateInfraID(infraID); err != nil {
		return nil, fmt.Errorf("%w (use --infra-id to specify a compliant identifier)", err)
	}

	issuerURL := fmt.Sprintf("%s/%s", opts.oidcEndpoint, infraID)

	fmt.Fprintln(w, ">>> Step 1: Setup IAM infrastructure")
	iamOpts := &iam.CreateOptions{
		ProjectID:     projectID,
		InfraID:       infraID,
		OIDCIssuerURL: issuerURL,
	}
	iamOutput, err := iamOpts.CreateIAM(ctx, logr.Discard())
	if err != nil {
		return nil, fmt.Errorf("IAM setup failed: %w", err)
	}
	fmt.Fprintf(w, "  IAM setup complete: %d service accounts created\n", len(iamOutput.ServiceAccounts))

	fmt.Fprintln(w, ">>> Step 2: Setup network infrastructure")
	networkOpts := &network.CreateOptions{
		ProjectID: projectID,
		Region:    region,
		InfraID:   infraID,
	}
	networkOutput, err := networkOpts.CreateNetwork(ctx, logr.Discard())
	if err != nil {
		return nil, fmt.Errorf("network setup failed: %w", err)
	}
	fmt.Fprintf(w, "  Network setup complete: VPC=%s, Subnet=%s\n", networkOutput.NetworkName, networkOutput.SubnetName)

	fmt.Fprintln(w, ">>> Step 3: Building cluster payload")
	return assemblePayload(clusterName, infraID, projectID, region, iamOutput, networkOutput, opts)
}

func validatePayloadInputs(projectID, region string, iamOutput *iam.CreateOutput, netOutput *network.CreateOutput) error {
	if projectID == "" {
		return fmt.Errorf("IAM config missing required field: projectId")
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

func assemblePayload(clusterName, infraID, projectID, region string, iamOutput *iam.CreateOutput, netOutput *network.CreateOutput, opts buildPayloadOptions) ([]byte, error) {
	if err := validatePayloadInputs(projectID, region, iamOutput, netOutput); err != nil {
		return nil, err
	}

	issuerURL := fmt.Sprintf("%s/%s", opts.oidcEndpoint, infraID)

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
		InfraID:   &infraID,
		IssuerURL: &issuerURL,
		Platform: hyperfleet.ClusterPlatformSpec{
			Type: "GCP",
			Gcp: hyperfleet.GCPClusterPlatform{
				ProjectID:        projectID,
				Region:           region,
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

	payload := map[string]interface{}{
		"name": clusterName,
		"spec": spec,
		"kind": "Cluster",
		"labels": map[string]string{
			"shard": "1",
		},
	}

	return json.Marshal(payload)
}
