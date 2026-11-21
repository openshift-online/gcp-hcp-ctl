package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
	"strconv"
	"strings"

	gcpiam "github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/iam"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	iam "google.golang.org/api/iam/v1"
)

// Cloud is the bounded resource/policy seam. Preview uses reads only; no generic
// arbitrary IAM operation, undelete, service enablement or trust repair exists.
type CloudReader interface {
	ProjectNumber(context.Context) (string, error)
	Get(context.Context, Resource) (*Resource, error)
	GetPolicy(context.Context, string) (*crm.Policy, error)
	CheckPermissions(context.Context, []string) error
}
type CloudWriter interface {
	Create(context.Context, Resource) (*Resource, error)
	Delete(context.Context, Resource) error
	SetPolicy(context.Context, string, *crm.Policy) error
}
type Cloud interface {
	CloudReader
	CloudWriter
}

// RolePermissionWriter cannot create resources or change bindings or trust.
type RolePermissionWriter interface {
	UpdateRolePermissions(context.Context, Resource) error
	CheckRoleUpdateAuthority(context.Context) error
}

func (c *GCP) CheckRoleUpdateAuthority(ctx context.Context) error {
	return cloudError(c.Client.TestPermissions(ctx, []string{"iam.roles.get", "iam.roles.update"}))
}

func (c *GCP) UpdateRolePermissions(ctx context.Context, desired Resource) error {
	if desired.Kind != "Role" || desired.UID == "" || desired.Owner == "" || len(desired.Permissions) == 0 || len(desired.Permissions) > 512 {
		return fmt.Errorf("owned role and fresh etag are required")
	}
	got, err := c.Get(ctx, desired)
	if err != nil {
		return err
	}
	if got.Owner != desired.Owner || got.UID != desired.UID || got.Stage != desired.Stage || got.Disabled || got.Deleted {
		return fmt.Errorf("role ownership or etag changed before permission update")
	}
	for _, permission := range got.Permissions {
		found := false
		for _, keep := range desired.Permissions {
			found = found || permission == keep
		}
		if !found {
			return fmt.Errorf("role update cannot remove an existing permission")
		}
	}
	_, err = c.Client.UpdateCustomRolePermissions(ctx, desired.Name, desired.UID, desired.Permissions)
	return cloudError(err)
}

type AsyncCloud interface {
	StartCreate(context.Context, Resource) (string, error)
	WaitCreate(context.Context, string) error
}

type CloudError struct{ Cause error }

func (e *CloudError) Error() string {
	var safe interface{ SafeReason() string }
	if errors.As(e.Cause, &safe) {
		return safe.SafeReason()
	}
	var api *googleapi.Error
	if errors.As(e.Cause, &api) {
		switch api.Code {
		case 401:
			return "cloud authentication required"
		case 403:
			return "cloud access denied"
		case 404:
			return "cloud resource not found"
		case 409, 412:
			return "cloud state conflict"
		case 429:
			return "cloud request throttled"
		}
	}
	return "cloud operation failed; inspect the operation journal and approved cloud diagnostics"
}
func (e *CloudError) Unwrap() error { return e.Cause }
func cloudError(err error) error {
	if err == nil {
		return nil
	}
	return &CloudError{Cause: err}
}
func notFound(err error) bool { var e *googleapi.Error; return errors.As(err, &e) && e.Code == 404 }
func conflict(err error) bool {
	var e *googleapi.Error
	return errors.As(err, &e) && (e.Code == 409 || e.Code == 412)
}

// Definitive classifies no-effect request rejections, not provider prose.
// Completed LRO failures are handled conservatively with a fresh absence check.
func Definitive(err error) bool {
	var e *googleapi.Error
	if errors.As(err, &e) {
		switch e.Code {
		case 400, 401, 403, 404, 422, 429:
			return true
		}
	}
	return false
}
func terminalOperation(err error) bool {
	var e interface{ Definitive() bool }
	return errors.As(err, &e) && e.Definitive()
}
func TerminalFailure(err error) bool { return terminalOperation(err) }

type GCP struct{ Client *gcpiam.Client }

func NewGCP(ctx context.Context, project string) (*GCP, error) {
	if !projectPattern.MatchString(project) {
		return nil, fmt.Errorf("invalid customer project ID")
	}
	c, err := gcpiam.NewClient(ctx, project)
	if err != nil {
		return nil, cloudError(err)
	}
	return &GCP{c}, nil
}
func (c *GCP) ProjectNumber(ctx context.Context) (string, error) {
	n, err := c.Client.GetProjectNumber(ctx)
	if err != nil {
		return "", cloudError(err)
	}
	return strconv.FormatInt(n, 10), nil
}
func (c *GCP) CheckPermissions(ctx context.Context, p []string) error {
	return cloudError(c.Client.CheckCustomPermissions(ctx, p))
}
func (c *GCP) CheckWriterAuthority(ctx context.Context) error {
	return cloudError(c.Client.CheckInstallerPermissions(ctx))
}
func (c *GCP) CheckCleanupAuthority(ctx context.Context) error {
	return cloudError(c.Client.CheckCleanupPermissions(ctx))
}

func (c *GCP) Get(ctx context.Context, want Resource) (*Resource, error) {
	r := &Resource{Kind: want.Kind, Name: want.Name}
	switch want.Kind {
	case "ServiceAccount":
		sa, err := c.Client.GetServiceAccount(ctx, want.Name)
		if err != nil {
			return nil, cloudError(err)
		}
		if sa.ProjectId != c.Client.ProjectID || sa.Email != strings.TrimPrefix(want.Name, "projects/"+c.Client.ProjectID+"/serviceAccounts/") {
			return nil, fmt.Errorf("service-account project or identity mismatch")
		}
		r.UID = sa.UniqueId
		r.Owner = sa.Description
		r.Disabled = sa.Disabled
	case "Role":
		role, err := c.Client.GetCustomRole(ctx, want.Name)
		if err != nil {
			return nil, cloudError(err)
		}
		if role.Name != want.Name {
			return nil, fmt.Errorf("custom-role identity mismatch")
		}
		r.UID = role.Etag
		r.Owner = role.Description
		r.Permissions = role.IncludedPermissions
		r.Deleted = role.Deleted
		r.Disabled = role.Stage == "DISABLED"
		r.Stage = role.Stage
	case "Pool":
		pool, err := c.Client.GetWorkloadIdentityPool(ctx, want.Name)
		if err != nil {
			return nil, cloudError(err)
		}
		if pool.Name != want.Name {
			return nil, fmt.Errorf("pool identity mismatch")
		}
		r.Owner = pool.Description
		r.Disabled = pool.Disabled
		r.Deleted = pool.State != "ACTIVE"
	case "Provider":
		provider, err := c.Client.GetWorkloadIdentityProvider(ctx, want.Name)
		if err != nil {
			return nil, cloudError(err)
		}
		if provider.Name != want.Name || provider.Oidc == nil {
			return nil, fmt.Errorf("provider identity or trust mismatch")
		}
		r.Owner = provider.Description
		r.Disabled = provider.Disabled
		r.Deleted = provider.State != "ACTIVE"
		r.Issuer = provider.Oidc.IssuerUri
		r.Audiences = provider.Oidc.AllowedAudiences
		r.InlineJWKS = provider.Oidc.JwksJson
		r.Mapping = provider.AttributeMapping
		r.Condition = provider.AttributeCondition
	default:
		return nil, fmt.Errorf("unsupported IAM resource kind")
	}
	return r, nil
}

func (c *GCP) Create(ctx context.Context, r Resource) (*Resource, error) {
	var err error
	last := r.Name[strings.LastIndex(r.Name, "/")+1:]
	switch r.Kind {
	case "ServiceAccount":
		var created *iam.ServiceAccount
		created, err = c.Client.CreateServiceAccount(ctx, strings.Split(last, "@")[0], &iam.ServiceAccount{Description: r.Owner})
		if err == nil {
			r.UID = created.UniqueId
			return &r, nil
		}
	case "Role":
		var created *iam.Role
		created, err = c.Client.CreateCustomRole(ctx, last, &iam.Role{Title: last, Description: r.Owner, Stage: r.Stage, IncludedPermissions: r.Permissions})
		if err == nil {
			r.UID = created.Etag
			return &r, nil
		}
	case "Pool", "Provider":
		return nil, fmt.Errorf("federation creation requires durable asynchronous operation recording")
	default:
		return nil, fmt.Errorf("unsupported IAM resource kind")
	}
	if err != nil {
		return nil, cloudError(err)
	}
	return c.Get(ctx, r)
}

func (c *GCP) StartCreate(ctx context.Context, r Resource) (string, error) {
	// SDK Create methods append the collection themselves: parent must be the
	// containing location (pool) or pool resource (provider), not its collection.
	var collection string
	switch r.Kind {
	case "Pool":
		collection = "/workloadIdentityPools/"
	case "Provider":
		collection = "/providers/"
	default:
		return "", fmt.Errorf("unsupported asynchronous resource")
	}
	parent, last, found := strings.Cut(r.Name, collection)
	if !found || parent == "" || last == "" || strings.Contains(last, "/") {
		return "", fmt.Errorf("invalid federation resource name")
	}
	var name string
	var err error
	switch r.Kind {
	case "Pool":
		name, err = c.Client.StartPool(ctx, parent, last, &iam.WorkloadIdentityPool{Description: r.Owner, DisplayName: last})
	case "Provider":
		name, err = c.Client.StartProvider(ctx, parent, last, &iam.WorkloadIdentityPoolProvider{Description: r.Owner, DisplayName: last, AttributeMapping: r.Mapping, Oidc: &iam.Oidc{IssuerUri: r.Issuer, AllowedAudiences: r.Audiences}})
	default:
		return "", fmt.Errorf("unsupported asynchronous resource")
	}
	return name, cloudError(err)
}
func (c *GCP) WaitCreate(ctx context.Context, name string) error {
	return cloudError(c.Client.WaitOperation(ctx, name))
}

// NoDependents checks actual VM account attachments before standalone IAM cleanup.
// Cluster/network lifecycle checks are additionally required by the command layer.
func (c *GCP) NoDependents(ctx context.Context, p *Plan) error {
	service, err := compute.NewService(ctx, option.WithScopes(compute.CloudPlatformScope))
	if err != nil {
		return cloudError(err)
	}
	emails := map[string]bool{}
	for _, email := range p.Accounts() {
		emails[email] = true
	}
	err = service.Instances.AggregatedList(c.Client.ProjectID).Pages(ctx, func(page *compute.InstanceAggregatedList) error {
		if len(page.Unreachables) > 0 || page.Warning != nil && page.Warning.Code != "NO_RESULTS_ON_PAGE" {
			return fmt.Errorf("complete instance inventory is required")
		}
		for _, zone := range page.Items {
			if zone.Warning != nil && zone.Warning.Code != "NO_RESULTS_ON_PAGE" {
				return fmt.Errorf("an instance scope could not be inspected")
			}
			for _, instance := range zone.Instances {
				for _, nic := range instance.NetworkInterfaces {
					if managedNetwork(nic.Network, p) {
						return fmt.Errorf("installation network still has cloud instances")
					}
				}
				for _, sa := range instance.ServiceAccounts {
					if emails[sa.Email] {
						return fmt.Errorf("managed component account is attached to a cloud instance")
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return cloudError(err)
	}
	err = service.ForwardingRules.AggregatedList(c.Client.ProjectID).Pages(ctx, func(page *compute.ForwardingRuleAggregatedList) error {
		if len(page.Unreachables) > 0 || page.Warning != nil && page.Warning.Code != "NO_RESULTS_ON_PAGE" {
			return fmt.Errorf("complete forwarding-rule inventory is required")
		}
		for _, region := range page.Items {
			if region.Warning != nil && region.Warning.Code != "NO_RESULTS_ON_PAGE" {
				return fmt.Errorf("a forwarding-rule scope could not be inspected")
			}
			for _, rule := range region.ForwardingRules {
				if managedNetwork(rule.Network, p) || managedSubnet(rule.Subnetwork, p) {
					return fmt.Errorf("installation still has cloud forwarding rules")
				}
			}
		}
		return nil
	})
	if err != nil {
		return cloudError(err)
	}
	err = service.Addresses.AggregatedList(c.Client.ProjectID).Pages(ctx, func(page *compute.AddressAggregatedList) error {
		if len(page.Unreachables) > 0 || page.Warning != nil && page.Warning.Code != "NO_RESULTS_ON_PAGE" {
			return fmt.Errorf("complete address inventory is required")
		}
		for _, region := range page.Items {
			if region.Warning != nil && region.Warning.Code != "NO_RESULTS_ON_PAGE" {
				return fmt.Errorf("an address scope could not be inspected")
			}
			for _, address := range region.Addresses {
				if managedNetwork(address.Network, p) || managedSubnet(address.Subnetwork, p) {
					return fmt.Errorf("installation still has cloud addresses")
				}
			}
		}
		return nil
	})
	return cloudError(err)
}

func managedNetwork(value string, p *Plan) bool {
	return projectResource(value, p) && strings.HasSuffix(value, "/global/networks/"+p.Identity.InfraID+"-network")
}
func managedSubnet(value string, p *Plan) bool {
	return projectResource(value, p) && strings.HasSuffix(value, "/subnetworks/"+p.Identity.InfraID+"-subnet")
}
func projectResource(value string, p *Plan) bool {
	return strings.Contains(value, "/projects/"+p.Identity.Project+"/") || strings.Contains(value, "/projects/"+p.Identity.ProjectNumber+"/")
}

func (c *GCP) Delete(ctx context.Context, r Resource) error {
	var err error
	switch r.Kind {
	case "ServiceAccount":
		if r.UID == "" {
			return fmt.Errorf("service-account unique ID is required for deletion")
		}
		err = c.Client.DeleteServiceAccount(ctx, "projects/"+c.Client.ProjectID+"/serviceAccounts/"+r.UID)
	case "Role":
		if r.UID == "" {
			return fmt.Errorf("custom-role etag is required for deletion")
		}
		err = c.Client.DeleteCustomRole(ctx, r.Name, r.UID)
	case "Provider":
		err = c.Client.DeleteWorkloadIdentityProvider(ctx, r.Name)
	case "Pool":
		err = c.Client.DeleteWorkloadIdentityPool(ctx, r.Name)
	default:
		return fmt.Errorf("unsupported IAM resource kind")
	}
	return cloudError(err)
}

func (c *GCP) GetPolicy(ctx context.Context, scope string) (*crm.Policy, error) {
	if scope == "project" {
		p, err := c.Client.GetProjectIAMPolicy(ctx)
		return p, cloudError(err)
	}
	p, err := c.Client.GetServiceAccountIAMPolicy(ctx, scope)
	if err != nil {
		return nil, cloudError(err)
	}
	var out crm.Policy
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("cannot encode service-account policy")
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("cannot decode service-account policy")
	}
	return &out, nil
}
func (c *GCP) SetPolicy(ctx context.Context, scope string, p *crm.Policy) error {
	if p == nil || p.Etag == "" || p.Version != 3 {
		return fmt.Errorf("policy version 3 and an optimistic etag are required")
	}
	if scope == "project" {
		return cloudError(c.Client.SetProjectIAMPolicy(ctx, p))
	}
	var out iam.Policy
	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("cannot encode service-account policy")
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("cannot decode service-account policy")
	}
	return cloudError(c.Client.SetServiceAccountIAMPolicy(ctx, scope, &out))
}
