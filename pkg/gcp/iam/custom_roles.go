package iam

import (
	"context"
	"time"

	"google.golang.org/api/iam/v1"
)

func (c *Client) GetServiceAccount(ctx context.Context, name string) (*iam.ServiceAccount, error) {
	return c.iamService.Projects.ServiceAccounts.Get(name).Context(ctx).Do()
}
func (c *Client) GetCustomRole(ctx context.Context, name string) (*iam.Role, error) {
	return c.iamService.Projects.Roles.Get(name).Context(ctx).Do()
}
func (c *Client) CreateCustomRole(ctx context.Context, id string, role *iam.Role) (*iam.Role, error) {
	return c.iamService.Projects.Roles.Create("projects/"+c.ProjectID, &iam.CreateRoleRequest{RoleId: id, Role: role}).Context(ctx).Do()
}
func (c *Client) UpdateCustomRole(ctx context.Context, name string, role *iam.Role) (*iam.Role, error) {
	return c.iamService.Projects.Roles.Patch(name, role).UpdateMask("includedPermissions,description,title,stage").Context(ctx).Do()
}

// UpdateCustomRolePermissions preserves title, description/ownership and stage.
// The service enforces the role's optimistic etag; callers must read it freshly.
func (c *Client) UpdateCustomRolePermissions(ctx context.Context, name, etag string, permissions []string) (*iam.Role, error) {
	return c.iamService.Projects.Roles.Patch(name, &iam.Role{Etag: etag, IncludedPermissions: permissions}).UpdateMask("includedPermissions").Context(ctx).Do()
}
func (c *Client) DeleteCustomRole(ctx context.Context, name, etag string) error {
	_, err := c.iamService.Projects.Roles.Delete(name).Etag(etag).Context(ctx).Do()
	return err
}

func (c *Client) StartPool(ctx context.Context, parent, id string, pool *iam.WorkloadIdentityPool) (string, error) {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Create(parent, pool).WorkloadIdentityPoolId(id).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return op.Name, nil
}
func (c *Client) StartProvider(ctx context.Context, parent, id string, provider *iam.WorkloadIdentityPoolProvider) (string, error) {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Providers.Create(parent, provider).WorkloadIdentityPoolProviderId(id).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return op.Name, nil
}
func (c *Client) WaitOperation(ctx context.Context, name string) error {
	return c.waitOperation(ctx, name)
}

// CheckCustomPermissions is read-only; unsupported/unknown permissions are not
// silently dropped or substituted with predefined roles.
func (c *Client) CheckCustomPermissions(ctx context.Context, permissions []string) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	wanted := map[string]bool{}
	for _, p := range permissions {
		wanted[p] = false
	}
	err := c.iamService.Permissions.QueryTestablePermissions(&iam.QueryTestablePermissionsRequest{FullResourceName: "//cloudresourcemanager.googleapis.com/projects/" + c.ProjectID, PageSize: 1000}).Pages(ctx, func(page *iam.QueryTestablePermissionsResponse) error {
		for _, p := range page.Permissions {
			// SUPPORTED is the protobuf enum's zero/default state and Google
			// omits it in normal JSON responses. Empty is not unknown here.
			// https://cloud.google.com/iam/docs/reference/rest/v1/permissions/queryTestablePermissions#CustomRolesSupportLevel
			supported := p.CustomRolesSupportLevel == "" || p.CustomRolesSupportLevel == "SUPPORTED" || p.CustomRolesSupportLevel == "TESTING"
			if _, ok := wanted[p.Name]; ok && supported && !p.OnlyInPredefinedRoles && !p.ApiDisabled {
				wanted[p.Name] = true
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, ok := range wanted {
		if !ok {
			return errUnsupportedPermissions{}
		}
	}
	return nil
}

type errUnsupportedPermissions struct{}

func (errUnsupportedPermissions) SafeReason() string {
	return "contract has unsupported or unavailable project custom-role permissions"
}

func (errUnsupportedPermissions) Error() string {
	return "contract has unsupported or unavailable project custom-role permissions"
}
