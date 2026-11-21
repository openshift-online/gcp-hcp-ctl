package network

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	gcpiam "github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/iam"
	gcpnetwork "github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/networking"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam/application"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

// OwnedPlan preserves existing network/firewall semantics but never adopts an
// existing name. Unlike the historical Manager, this path journals every insert.
type OwnedPlan struct{ Project, Region, InfraID, Version, CIDR string }

func (p OwnedPlan) Fingerprint() string {
	data, _ := json.Marshal(p)
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}
func (p OwnedPlan) Output() *CreateOutput {
	network := p.InfraID + "-network"
	subnet := p.InfraID + "-subnet"
	base := "https://www.googleapis.com/compute/v1/projects/" + p.Project
	o := &CreateOutput{ProjectID: p.Project, Region: p.Region, InfraID: p.InfraID, NetworkName: network, NetworkSelfLink: base + "/global/networks/" + network, SubnetName: subnet, SubnetSelfLink: base + "/regions/" + p.Region + "/subnetworks/" + subnet, SubnetCIDR: p.CIDR, RouterName: p.InfraID + "-router", NATName: p.InfraID + "-nat"}
	if shouldCreateFirewallRule(p.Version, false) {
		o.FirewallRuleName = p.InfraID + "-internal-cluster"
	}
	return o
}
func (p OwnedPlan) Kinds() []string {
	kinds := []string{"Network", "Subnet", "Router"}
	if p.Output().FirewallRuleName != "" {
		kinds = append(kinds, "Firewall")
	}
	return kinds
}
func (p OwnedPlan) Name(kind string) string {
	o := p.Output()
	switch kind {
	case "Network":
		return o.NetworkName
	case "Subnet":
		return o.SubnetName
	case "Router":
		return o.RouterName
	case "Firewall":
		return o.FirewallRuleName
	}
	return ""
}
func (p OwnedPlan) Validate() error {
	ip, subnet, err := net.ParseCIDR(p.CIDR)
	if err != nil || ip.To4() == nil || subnet.String() != p.CIDR {
		return fmt.Errorf("an explicit canonical IPv4 subnet CIDR is required")
	}
	if !regexp.MustCompile(`^[a-z]+(-[a-z]+)+[0-9]+$`).MatchString(p.Region) {
		return fmt.Errorf("invalid network region")
	}
	return (&CreateOptions{ProjectID: p.Project, Region: p.Region, InfraID: p.InfraID, Version: p.Version, VPCCidr: p.CIDR, NetworkType: DefaultNetworkType, NodePortRange: DefaultNodePortRange}).ValidateInputs()
}

type Observed struct {
	ID, Owner string
	Matches   bool
}
type OwnedCloud interface {
	Read(context.Context, OwnedPlan, string, string) (*Observed, error)
	Start(context.Context, OwnedPlan, string, string) (string, error)
	Wait(context.Context, string, string) error
}
type OperationTargeter interface {
	TargetID(context.Context, OwnedPlan, string, string) (string, error)
}
type OwnedGCP struct {
	Client    *gcpnetwork.Client
	Authority *gcpiam.Client
}

func NewOwnedGCP(ctx context.Context, p OwnedPlan) (*OwnedGCP, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	c, err := gcpnetwork.NewClient(ctx, p.Project, p.Region)
	if err != nil {
		return nil, &application.CloudError{Cause: err}
	}
	authority, err := gcpiam.NewClient(ctx, p.Project)
	if err != nil {
		return nil, &application.CloudError{Cause: err}
	}
	return &OwnedGCP{Client: c, Authority: authority}, nil
}
func nat(p OwnedPlan) *compute.RouterNat {
	o := p.Output()
	return &compute.RouterNat{Name: o.NATName, NatIpAllocateOption: "AUTO_ONLY", SourceSubnetworkIpRangesToNat: "LIST_OF_SUBNETWORKS", Subnetworks: []*compute.RouterNatSubnetworkToNat{{Name: o.SubnetSelfLink, SourceIpRangesToNat: []string{"ALL_IP_RANGES"}}}}
}
func (c *OwnedGCP) Read(ctx context.Context, p OwnedPlan, kind, owner string) (*Observed, error) {
	o := p.Output()
	result := &Observed{}
	switch kind {
	case "Network":
		v, err := c.Client.GetNetwork(ctx, o.NetworkName)
		if err != nil {
			return nil, err
		}
		result.ID = strconv.FormatUint(v.Id, 10)
		result.Owner = v.Description
		result.Matches = !v.AutoCreateSubnetworks && len(v.Peerings) == 0
	case "Subnet":
		v, err := c.Client.GetSubnet(ctx, o.SubnetName)
		if err != nil {
			return nil, err
		}
		result.ID = strconv.FormatUint(v.Id, 10)
		result.Owner = v.Description
		result.Matches = v.Network == o.NetworkSelfLink && v.IpCidrRange == p.CIDR && v.PrivateIpGoogleAccess
	case "Router":
		v, err := c.Client.GetRouter(ctx, o.RouterName)
		if err != nil {
			return nil, err
		}
		result.ID = strconv.FormatUint(v.Id, 10)
		result.Owner = v.Description
		result.Matches = v.Network == o.NetworkSelfLink && len(v.Nats) == 1 && v.Nats[0].Name == o.NATName && v.Nats[0].NatIpAllocateOption == "AUTO_ONLY" && v.Nats[0].SourceSubnetworkIpRangesToNat == "LIST_OF_SUBNETWORKS" && len(v.Nats[0].Subnetworks) == 1 && v.Nats[0].Subnetworks[0].Name == o.SubnetSelfLink && reflect.DeepEqual(v.Nats[0].Subnetworks[0].SourceIpRangesToNat, []string{"ALL_IP_RANGES"})
	case "Firewall":
		v, err := c.Client.GetFirewall(ctx, o.FirewallRuleName)
		if err != nil {
			return nil, err
		}
		want := desiredFirewall(p.InfraID, o.NetworkSelfLink, DefaultNetworkType, DefaultNodePortRange)
		result.ID = strconv.FormatUint(v.Id, 10)
		result.Owner = v.Description
		result.Matches = v.Network == want.Network && v.Direction == want.Direction && v.Priority == want.Priority && !v.Disabled && reflect.DeepEqual(v.Allowed, want.Allowed) && reflect.DeepEqual(v.SourceTags, want.SourceTags) && reflect.DeepEqual(v.TargetTags, want.TargetTags) && len(v.SourceRanges) == 0 && len(v.DestinationRanges) == 0 && len(v.SourceServiceAccounts) == 0 && len(v.TargetServiceAccounts) == 0 && len(v.Denied) == 0
	default:
		return nil, fmt.Errorf("unsupported owned network resource")
	}
	return result, nil
}
func (c *OwnedGCP) Start(ctx context.Context, p OwnedPlan, kind, owner string) (string, error) {
	o := p.Output()
	switch kind {
	case "Network":
		return c.Client.StartNetwork(ctx, &compute.Network{Name: o.NetworkName, Description: owner, AutoCreateSubnetworks: false, ForceSendFields: []string{"AutoCreateSubnetworks"}})
	case "Subnet":
		return c.Client.StartSubnet(ctx, &compute.Subnetwork{Name: o.SubnetName, Description: owner, Network: o.NetworkSelfLink, IpCidrRange: p.CIDR, PrivateIpGoogleAccess: true})
	case "Router":
		return c.Client.StartRouter(ctx, &compute.Router{Name: o.RouterName, Description: owner, Network: o.NetworkSelfLink, Nats: []*compute.RouterNat{nat(p)}})
	case "Firewall":
		v := desiredFirewall(p.InfraID, o.NetworkSelfLink, DefaultNetworkType, DefaultNodePortRange)
		marker, err := ownershipMarker(p.InfraID)
		if err != nil {
			return "", err
		}
		v.Description = marker
		return c.Client.StartFirewall(ctx, v)
	}
	return "", fmt.Errorf("unsupported network resource")
}
func (c *OwnedGCP) Wait(ctx context.Context, kind, op string) error {
	return c.Client.WaitOwned(ctx, kind, op)
}
func (c *OwnedGCP) TargetID(ctx context.Context, p OwnedPlan, kind, op string) (string, error) {
	return c.Client.OwnedOperationTarget(ctx, kind, op)
}
func (c *OwnedGCP) RegionReady(ctx context.Context, newRouter bool) error {
	return c.Client.RegionReady(ctx, newRouter)
}
func (c *OwnedGCP) CheckAuthority(ctx context.Context, p OwnedPlan) error {
	if c.Authority == nil {
		return fmt.Errorf("network authority inspection is required")
	}
	permissions := []string{"compute.networks.get", "compute.networks.create", "compute.networks.delete", "compute.subnetworks.get", "compute.subnetworks.create", "compute.subnetworks.delete", "compute.routers.get", "compute.routers.create", "compute.routers.delete", "compute.globalOperations.get", "compute.regionOperations.get"}
	if p.Output().FirewallRuleName != "" {
		permissions = append(permissions, "compute.firewalls.get", "compute.firewalls.create", "compute.firewalls.delete")
	}
	return c.Authority.TestPermissions(ctx, permissions)
}
func (c *OwnedGCP) CheckCleanupAuthority(ctx context.Context, p OwnedPlan) error {
	if c.Authority == nil {
		return fmt.Errorf("network cleanup authority inspection is required")
	}
	permissions := []string{"compute.networks.get", "compute.networks.delete", "compute.subnetworks.get", "compute.subnetworks.delete", "compute.routers.get", "compute.routers.delete", "compute.globalOperations.get", "compute.regionOperations.get"}
	if p.Output().FirewallRuleName != "" {
		permissions = append(permissions, "compute.firewalls.get", "compute.firewalls.delete")
	}
	return c.Authority.TestPermissions(ctx, permissions)
}
func missing(err error) bool {
	var api *googleapi.Error
	return errors.As(err, &api) && api.Code == 404
}
func expectedOwner(p OwnedPlan, kind, owner string) string {
	if kind == "Firewall" {
		marker, _ := ownershipMarker(p.InfraID)
		return marker
	}
	return owner
}

func InspectOwned(ctx context.Context, c OwnedCloud, p OwnedPlan, j *application.Journal) error {
	if readiness, ok := c.(interface {
		RegionReady(context.Context, bool) error
	}); ok {
		newRouter := j == nil || j.Steps["network/Router"] == nil || j.Steps["network/Router"].State == "Planned" || j.Steps["network/Router"].State == "Failed"
		if err := readiness.RegionReady(ctx, newRouter); err != nil {
			return &application.CloudError{Cause: err}
		}
	}
	if authority, ok := c.(interface {
		CheckAuthority(context.Context, OwnedPlan) error
	}); ok {
		if err := authority.CheckAuthority(ctx, p); err != nil {
			return &application.CloudError{Cause: err}
		}
	}
	if err := p.Validate(); err != nil {
		return err
	}
	for _, kind := range p.Kinds() {
		owner := ""
		var step *application.Step
		if j != nil {
			owner = j.Owner()
			step = j.Steps["network/"+kind]
		}
		v, err := c.Read(ctx, p, kind, owner)
		if missing(err) {
			if step != nil && step.State != "Planned" && step.State != "Failed" && (step.Resource == nil || step.Resource.Operation == "") {
				return fmt.Errorf("attempted network resource is absent or still uncertain")
			}
			continue
		}
		if err != nil {
			return &application.CloudError{Cause: err}
		}
		if j == nil || step == nil || step.State == "Planned" || step.State == "Failed" || step.Resource == nil || (step.Resource.UID == "" && step.Resource.Operation == "") || (step.Resource.UID != "" && step.Resource.UID != v.ID) || v.Owner != expectedOwner(p, kind, owner) || !v.Matches {
			return fmt.Errorf("network collision or changed ownership; refusing adoption")
		}
	}
	return nil
}

func ProvisionOwned(ctx context.Context, c OwnedCloud, p OwnedPlan, s *application.Session) error {
	j := s.File.Journal
	if j.NetworkFingerprint != "" && j.NetworkFingerprint != p.Fingerprint() {
		return fmt.Errorf("network plan changed")
	}
	j.NetworkFingerprint = p.Fingerprint()
	if j.NetworkExternal {
		return fmt.Errorf("explicit external network cannot be adopted or modified")
	}
	if err := s.Verify(ctx); err != nil {
		return err
	}
	for _, kind := range p.Kinds() {
		if err := application.Recheck(ctx, s.Catalog, &j.Plan.Target); err != nil {
			return err
		}
		key := "network/" + kind
		step := j.Steps[key]
		if step == nil {
			step = &application.Step{State: "Planned"}
			j.Steps[key] = step
		}
		if step.State == "Failed" {
			step.State = "Planned"
			step.Resource = nil
		}
		if step.State == "Planned" {
			if _, err := c.Read(ctx, p, kind, j.Owner()); !missing(err) {
				if err != nil {
					return &application.CloudError{Cause: err}
				}
				return fmt.Errorf("network resource appeared after preflight")
			}
			step.State = "Intent"
			step.IntentAt = time.Now().UTC()
			step.Resource = &application.Resource{Kind: kind, Name: p.Name(kind)}
			if err := s.File.Save(j); err != nil {
				return err
			}
			op, err := c.Start(ctx, p, kind, j.Owner())
			step.Resource.Operation = op
			if err != nil {
				step.State = "Unknown"
				if application.Definitive(err) {
					step.State = "Failed"
					step.Resource = nil
				}
				if saveErr := s.File.Save(j); saveErr != nil {
					return saveErr
				}
				return &application.CloudError{Cause: err}
			}
			if err := s.File.Save(j); err != nil {
				return err
			}
		}
		if step.Resource == nil || step.Resource.Operation == "" {
			return fmt.Errorf("uncertain network insert has no operation identity; refusing retry")
		}
		if step.State != "Verified" {
			if err := c.Wait(ctx, kind, step.Resource.Operation); err != nil {
				if application.TerminalFailure(err) {
					if _, readErr := c.Read(ctx, p, kind, j.Owner()); missing(readErr) {
						step.State = "Failed"
						step.Resource = nil
						if saveErr := s.File.Save(j); saveErr != nil {
							return saveErr
						}
					}
				}
				return &application.CloudError{Cause: err}
			}
		}
		v, err := c.Read(ctx, p, kind, j.Owner())
		if err != nil {
			return &application.CloudError{Cause: err}
		}
		if !v.Matches || v.Owner != expectedOwner(p, kind, j.Owner()) || v.ID == "" || v.ID == "0" || (step.Resource.UID != "" && step.Resource.UID != v.ID) {
			return fmt.Errorf("network readback does not match the owned operation")
		}
		if step.State != "Verified" {
			targeter, ok := c.(OperationTargeter)
			if !ok {
				return fmt.Errorf("network insert target identity inspection is required")
			}
			id, err := targeter.TargetID(ctx, p, kind, step.Resource.Operation)
			if err != nil {
				return &application.CloudError{Cause: err}
			}
			if id != v.ID {
				return fmt.Errorf("network resource is not the target of the journaled insert")
			}
		}
		step.Resource.UID = v.ID
		step.State = "Verified"
		if err := s.File.Save(j); err != nil {
			return err
		}
	}
	j.Phase = "NetworkVerified"
	return s.File.Save(j)
}

// IdentityFromConfig verifies the existing config points to this exact plan;
// it never changes the selected project/infra/region to match an input file.
func (p OwnedPlan) MatchesConfig(o *CreateOutput) bool {
	want := p.Output()
	return o.ProjectID == want.ProjectID && o.Region == want.Region && o.InfraID == want.InfraID && o.NetworkName == want.NetworkName && o.SubnetName == want.SubnetName && strings.TrimRight(o.NetworkSelfLink, "/") == want.NetworkSelfLink && strings.TrimRight(o.SubnetSelfLink, "/") == want.SubnetSelfLink && o.SubnetCIDR == want.SubnetCIDR && o.RouterName == want.RouterName && o.NATName == want.NATName && o.FirewallRuleName == want.FirewallRuleName
}

// InspectExternal verifies explicitly supplied network inputs without adopting
// them. Their IDs are pinned for use only and must never be cleanup targets.
func InspectExternal(ctx context.Context, c OwnedCloud, p OwnedPlan, j *application.Journal) (map[string]*Observed, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	out := map[string]*Observed{}
	for _, kind := range p.Kinds() {
		v, err := c.Read(ctx, p, kind, "")
		if err != nil {
			return nil, &application.CloudError{Cause: err}
		}
		if !v.Matches || v.ID == "" || v.ID == "0" {
			return nil, fmt.Errorf("external network does not match the explicit configuration")
		}
		if j != nil {
			if step := j.Steps["network/"+kind]; step != nil && step.Resource != nil && step.Resource.UID != v.ID {
				return nil, fmt.Errorf("external network identity changed")
			}
		}
		out[kind] = v
	}
	return out, nil
}
