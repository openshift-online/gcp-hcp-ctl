package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam/application"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/network"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const maxInfraIDLength = 12

var (
	sanitizePattern     = regexp.MustCompile(`[^a-z0-9-]`)
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
	subnetCIDR        string
	now               func() time.Time
	infraID           string
	stateFile         string
	expectPlan        string
	confirm           bool
	resume            bool
	maintenanceUntil  string
	iamCloud          application.Cloud
	networkCloud      network.OwnedCloud
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
		Long: `Create a fresh cluster using its authenticated, pinned IAM contract.

Preview without changes:
  gcphcpctl cluster create my-cluster --setup-infra --infra-id fresh-123 \
    --project my-project --version 5.0.0 --dry-run

After reviewing and authorizing the complete plan, mutation additionally requires
--confirm --expect-plan <fingerprint> --state-file <private-path>.
Existing operation files require --resume. An uncertain submission is resolved
by exact identity rather than provisioning a second installation.

Config-file mode requires both --iam-config-file and --network-config-file plus
the owned --state-file. Files are identity hints, not fresh IAM verification;
the configured existing network is read-only and is never adopted for cleanup.`,
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
	cmd.Flags().StringVar(&opts.channelGroup, "channel-group", "stable", "Release channel group")
	_ = cmd.Flags().MarkHidden("channel-group")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Show payload without creating")
	cmd.Flags().StringVarP(&opts.outputFmt, "output", "o", "text", "Output format: text, json, yaml")
	cmd.Flags().StringVar(&opts.infraID, "infra-id", "", "Stable installation ID (required for confirmed setup)")
	cmd.Flags().StringVar(&opts.stateFile, "state-file", "", "Protected operation journal")
	cmd.Flags().StringVar(&opts.expectPlan, "expect-plan", "", "Expected complete preview fingerprint")
	cmd.Flags().BoolVar(&opts.confirm, "confirm", false, "Confirm the exact previewed installation")
	cmd.Flags().BoolVar(&opts.resume, "resume", false, "Explicitly resume the same operation")
	cmd.Flags().StringVar(&opts.maintenanceUntil, "maintenance-exclusion-until", "", "Initial automatic-upgrade exclusion expiry (RFC3339)")
	cmd.Flags().StringVar(&opts.subnetCIDR, "vpc-cidr", network.DefaultSubnetCIDR, "Explicit IPv4 subnet CIDR for the installation plan")

	return cmd
}

func validateVersion(ctx context.Context, versions platformapi.VersionInterface, version, channelGroup string) error {
	release, err := versions.Get(ctx, version)
	var httpErr *platformapi.HTTPError
	if apierrors.IsNotFound(err) || (errors.As(err, &httpErr) && httpErr.StatusCode() == http.StatusNotFound) {
		return fmt.Errorf("version %q is not supported", version)
	}
	if err != nil {
		return fmt.Errorf("validating version %q: %w", version, err)
	}

	for _, group := range release.Spec.ChannelGroups {
		if group == channelGroup {
			return nil
		}
	}

	return fmt.Errorf("version %q is not available in channel group %q", version, channelGroup)
}

// decorateCreateError adds infrastructure guidance only after setup-infra has
// completed and the subsequent create has an uncertain outcome. In particular,
// rerunning setup-infra blindly could provision a second set of resources.
func decorateCreateError(err error, setupInfra bool) error {
	if err == nil {
		return nil
	}
	wrapped := fmt.Errorf("creating cluster: %w", err)
	if setupInfra && platformapi.IsUncertainOutcome(err) {
		return fmt.Errorf("%w; inspect the cluster and provisioned IAM/network resources before retrying or cleaning up", wrapped)
	}
	return wrapped
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

func buildPayloadFromConfigs(iamConfigFile, networkConfigFile string, opts buildPayloadOptions) (*gcpv1.Cluster, error) {
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

// networkCreateOptions builds the network setup options for --setup-infra. The
// requested cluster version decides who owns the worker firewall rule from
// 5.0.0 GA onwards (GCP-1349).
func networkCreateOptions(opts buildPayloadOptions) *network.CreateOptions {
	return &network.CreateOptions{
		ProjectID: opts.projectID,
		Region:    opts.region,
		InfraID:   opts.infraID,
		Version:   opts.version,
	}
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

func assemblePayload(iamOutput *iam.CreateOutput, netOutput *network.CreateOutput, opts buildPayloadOptions) (*gcpv1.Cluster, error) {
	if err := validatePayloadInputs(opts.projectID, opts.region, iamOutput, netOutput); err != nil {
		return nil, err
	}

	issuerURL := opts.issuerURL()

	cluster := &gcpv1.Cluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: gcpv1.GroupVersion.String(),
			Kind:       "Cluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: opts.clusterName,
		},
		Spec: gcpv1.ClusterSpec{
			InfraID:   opts.infraID,
			IssuerURL: issuerURL,
			Platform: gcpv1.ClusterPlatformSpec{
				Type: "GCP",
				GCP: &gcpv1.GCPClusterPlatform{
					ProjectID:      opts.projectID,
					Region:         opts.region,
					Network:        netOutput.NetworkName,
					Subnet:         netOutput.SubnetName,
					EndpointAccess: opts.endpointAccess,
					WorkloadIdentity: gcpv1.WorkloadIdentitySpec{
						ProjectNumber: iamOutput.ProjectNumber,
						PoolID:        iamOutput.WorkloadIdentityPool.PoolID,
						ProviderID:    iamOutput.WorkloadIdentityPool.ProviderID,
						ServiceAccountsRef: &gcpv1.ServiceAccountsRef{
							ControlPlaneEmail:    iamOutput.ServiceAccounts["ctrlplane-op"],
							NodePoolEmail:        iamOutput.ServiceAccounts["nodepool-mgmt"],
							CloudControllerEmail: iamOutput.ServiceAccounts["cloud-controller"],
							StorageEmail:         iamOutput.ServiceAccounts["gcp-pd-csi"],
							ImageRegistryEmail:   iamOutput.ServiceAccounts["image-registry"],
							NetworkEmail:         iamOutput.ServiceAccounts["cloud-network"],
						},
					},
				},
			},
			Release: gcpv1.ClusterReleaseSpec{
				Version:      opts.version,
				ChannelGroup: opts.channelGroup,
			},
		},
	}

	return cluster, nil
}
