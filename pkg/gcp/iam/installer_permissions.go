package iam

import (
	"context"
	"fmt"
	crm "google.golang.org/api/cloudresourcemanager/v1"
)

// InstallerPermissions describes caller authority, never component role grants.
func InstallerPermissions() []string {
	return []string{
		"resourcemanager.projects.get", "resourcemanager.projects.getIamPolicy", "resourcemanager.projects.setIamPolicy",
		"iam.roles.create", "iam.roles.get", "iam.roles.delete", "iam.roles.update",
		"iam.serviceAccounts.create", "iam.serviceAccounts.get", "iam.serviceAccounts.delete", "iam.serviceAccounts.getIamPolicy", "iam.serviceAccounts.setIamPolicy",
		"iam.workloadIdentityPools.create", "iam.workloadIdentityPools.get", "iam.workloadIdentityPools.delete",
		"iam.workloadIdentityPoolProviders.create", "iam.workloadIdentityPoolProviders.get", "iam.workloadIdentityPoolProviders.delete",
	}
}
func (c *Client) CheckInstallerPermissions(ctx context.Context) error {
	return c.TestPermissions(ctx, InstallerPermissions())
}
func (c *Client) CheckCleanupPermissions(ctx context.Context) error {
	return c.TestPermissions(ctx, []string{"resourcemanager.projects.get", "resourcemanager.projects.getIamPolicy", "resourcemanager.projects.setIamPolicy", "iam.roles.get", "iam.roles.delete", "iam.serviceAccounts.get", "iam.serviceAccounts.delete", "iam.serviceAccounts.getIamPolicy", "iam.serviceAccounts.setIamPolicy", "iam.workloadIdentityPools.get", "iam.workloadIdentityPools.delete", "iam.workloadIdentityPoolProviders.get", "iam.workloadIdentityPoolProviders.delete"})
}
func (c *Client) TestPermissions(ctx context.Context, wanted []string) error {
	response, err := c.crmService.Projects.TestIamPermissions(c.ProjectID, &crm.TestIamPermissionsRequest{Permissions: wanted}).Context(ctx).Do()
	if err != nil {
		return err
	}
	available := map[string]bool{}
	for _, p := range response.Permissions {
		available[p] = true
	}
	for _, p := range wanted {
		if !available[p] {
			return fmt.Errorf("required installer/cleanup permission is unavailable")
		}
	}
	return nil
}
