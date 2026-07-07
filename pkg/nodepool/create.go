package nodepool

import (
	"fmt"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/hyperfleet"
	"github.com/spf13/cobra"
)

type createOptions struct {
	clusterRef   string
	replicas     int
	instanceType string
	diskSize     int
	diskType     string
	zone         string
	version      string
	channelGroup string
	outputFmt    string
}

func newCreateCmd() *cobra.Command {
	opts := &createOptions{}

	cmd := &cobra.Command{
		Use:   "create <nodepool-name>",
		Short: "Create a nodepool",
		Long: `Create a nodepool in a cluster via the HyperFleet API.

  gcphcpctl nodepool create my-nodepool --cluster my-cluster --replicas 2
  gcphcpctl nodepool create workers --cluster my-cluster \
    --replicas 3 --instance-type n2-standard-8 --disk-size 200
  gcphcpctl nodepool create workers --cluster my-cluster \
    --replicas 2 --version 4.22.0-rc.5 --channel-group candidate`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("nodepool name is required\n\nUsage: %s", cmd.UseLine())
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(cmd, args[0])
		},
	}

	cmd.Flags().StringVar(&opts.clusterRef, "cluster", "", "Cluster name or ID (required)")
	cmd.Flags().IntVar(&opts.replicas, "replicas", 2, "Number of replicas")
	cmd.Flags().StringVar(&opts.instanceType, "instance-type", "n2-standard-4", "GCE machine type")
	cmd.Flags().IntVar(&opts.diskSize, "disk-size", 100, "Boot disk size in GB")
	cmd.Flags().StringVar(&opts.diskType, "disk-type", "pd-balanced", "Boot disk type: pd-standard, pd-ssd, pd-balanced")
	cmd.Flags().StringVar(&opts.zone, "zone", "", "GCP zone (defaults to cluster region + \"-a\")")
	cmd.Flags().StringVar(&opts.version, "version", "", "OCP version (e.g. 4.22.0-rc.5); defaults to cluster version")
	cmd.Flags().StringVar(&opts.channelGroup, "channel-group", "", "Channel group: stable, fast, candidate, eus; defaults to cluster channel")
	cmd.Flags().StringVarP(&opts.outputFmt, "output", "o", "text", "Output format: text, json, yaml")

	_ = cmd.MarkFlagRequired("cluster")

	return cmd
}

func (o *createOptions) run(cmd *cobra.Command, npName string) error {
	switch o.diskType {
	case "pd-standard", "pd-ssd", "pd-balanced":
	default:
		return fmt.Errorf("--disk-type must be one of: pd-standard, pd-ssd, pd-balanced")
	}
	if o.replicas < 0 {
		return fmt.Errorf("--replicas must be non-negative")
	}

	client := clientFromCmd(cmd)
	ctx := cmd.Context()

	cluster, err := resolveCluster(ctx, client, o.clusterRef)
	if err != nil {
		return err
	}
	clusterID := ptrStr(cluster.Id)
	if clusterID == "" {
		return fmt.Errorf("cluster %q has no ID", o.clusterRef)
	}

	diskType := hyperfleet.GCPRootVolumeType(o.diskType)
	labels := map[string]string{"shard": defaultShardLabel}
	req := hyperfleet.CreateNodePoolJSONRequestBody{
		Name:   npName,
		Kind:   strPtr("NodePool"),
		Labels: &labels,
		Spec: hyperfleet.NodePoolSpec{
			Replicas: intPtr(o.replicas),
			Platform: hyperfleet.NodePoolPlatformSpec{
				Type: "gcp",
				Gcp: hyperfleet.GCPNodePoolPlatform{
					InstanceType: strPtr(o.instanceType),
					RootVolume: &hyperfleet.GCPRootVolume{
						Size: intPtr(o.diskSize),
						Type: &diskType,
					},
				},
			},
		},
	}

	if o.zone != "" {
		req.Spec.Platform.Gcp.Zone = strPtr(o.zone)
	}

	if o.version != "" || o.channelGroup != "" {
		req.Spec.Release = &hyperfleet.NodePoolReleaseSpec{}
		if o.version != "" {
			req.Spec.Release.Version = strPtr(o.version)
		}
		if o.channelGroup != "" {
			req.Spec.Release.ChannelGroup = strPtr(o.channelGroup)
		}
	}

	resp, err := client.CreateNodePoolWithResponse(ctx, clusterID, req)
	if err != nil {
		return fmt.Errorf("creating nodepool: %w", err)
	}
	if resp.JSON201 == nil {
		return fmt.Errorf("creating nodepool: %s", formatError(resp.HTTPResponse, resp.Body))
	}

	return printNodePool(cmd.OutOrStdout(), nodePoolFromCreateResponse(resp.JSON201), o.outputFmt)
}

func nodePoolFromCreateResponse(cr *hyperfleet.NodePoolCreateResponse) *hyperfleet.NodePool {
	return &hyperfleet.NodePool{
		CreatedBy:       cr.CreatedBy,
		CreatedTime:     cr.CreatedTime,
		DeletedBy:       cr.DeletedBy,
		DeletedTime:     cr.DeletedTime,
		Generation:      cr.Generation,
		Href:            cr.Href,
		Id:              cr.Id,
		Kind:            cr.Kind,
		Labels:          cr.Labels,
		Name:            cr.Name,
		OwnerReferences: cr.OwnerReferences,
		Spec:            cr.Spec,
		Status:          cr.Status,
		UpdatedBy:       cr.UpdatedBy,
		UpdatedTime:     cr.UpdatedTime,
	}
}
