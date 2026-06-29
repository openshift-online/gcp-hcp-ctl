package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/go-logr/logr"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/hyperfleet"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/network"
	"github.com/spf13/cobra"
)

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
       --version 4.22.0-rc.5

  2. Setup infra: provision IAM and network automatically, then create cluster
     gcphcpctl cluster create my-cluster \
       --setup-infra --project my-project \
       --version 4.22.0-rc.5`,
		Args:         cobra.ExactArgs(1),
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
	oidcBase, _ := cmd.Flags().GetString("oidc-endpoint")
	if oidcBase == "" {
		return fmt.Errorf("--oidc-endpoint is required (or set GCPHCPCTL_OIDC_ENDPOINT or oidc_endpoint in config)")
	}

	apiEndpoint, _ := cmd.Flags().GetString("api-endpoint")
	client, err := newClient(apiEndpoint)
	if err != nil {
		return err
	}

	payloadOpts := buildPayloadOptions{
		endpointAccess: o.endpointAccess,
		oidcEndpoint:   oidcBase,
		version:        o.version,
		channelGroup:   o.channelGroup,
	}

	var payload []byte

	switch {
	case o.iamConfigFile != "":
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
		return fmt.Errorf("either --setup-infra or --iam-config-file (with optional --network-config-file) is required")
	}

	if o.dryRun {
		fmt.Println("Dry run — would POST this payload:")
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, payload, "", "  "); err != nil {
			os.Stdout.Write(payload)
		} else {
			pretty.WriteTo(os.Stdout)
		}
		fmt.Println()
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

func buildPayloadFromConfigs(clusterName, iamConfigFile, networkConfigFile, defaultRegion string, opts buildPayloadOptions) ([]byte, error) {
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
	projectID := iamConfig.ProjectID
	region := defaultRegion
	if region == "" {
		region = "us-central1"
	}
	var network, subnet *string

	if networkConfigFile != "" {
		networkData, err := os.ReadFile(networkConfigFile)
		if err != nil {
			return nil, fmt.Errorf("reading network config: %w", err)
		}
		var infra struct {
			Region      string `json:"region"`
			ProjectID   string `json:"projectId"`
			NetworkName string `json:"networkName"`
			SubnetName  string `json:"subnetName"`
		}
		if err := json.Unmarshal(networkData, &infra); err != nil {
			return nil, fmt.Errorf("parsing network config: %w", err)
		}
		if infra.Region != "" {
			region = infra.Region
		}
		if infra.NetworkName != "" {
			network = &infra.NetworkName
		}
		if infra.SubnetName != "" {
			subnet = &infra.SubnetName
		}
	}

	return assemblePayload(clusterName, infraID, projectID, region, network, subnet, &iamConfig, opts)
}

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
	return assemblePayload(clusterName, infraID, projectID, region, &networkOutput.NetworkName, &networkOutput.SubnetName, iamOutput, opts)
}

func assemblePayload(clusterName, infraID, projectID, region string, network, subnet *string, iamOutput *iam.CreateOutput, opts buildPayloadOptions) ([]byte, error) {
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
				Network:          network,
				Subnet:           subnet,
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
