// Package iam provides a client for managing GCP IAM resources
// including Workload Identity Pools, OIDC Providers, and Service Accounts.
package iam

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

const (
	operationTimeout      = 5 * time.Minute
	operationPollInterval = 2 * time.Second
)

func wrapAuthError(action string, err error) error {
	return &RequestError{Action: action, Cause: err}
}

type RequestError struct {
	Action string
	Cause  error
}

func (e *RequestError) Error() string {
	label := "cloud request failed"
	var api *googleapi.Error
	if errors.As(e.Cause, &api) {
		switch api.Code {
		case 401:
			label = "cloud authentication required"
		case 403:
			label = "cloud access denied"
		case 404:
			label = "cloud resource not found"
		case 409, 412:
			label = "cloud state conflict"
		case 429:
			label = "cloud request throttled"
		}
	}
	return e.Action + ": " + label
}
func (e *RequestError) Unwrap() error { return e.Cause }

type OperationFailure struct{ Code int64 }

func (e *OperationFailure) Error() string    { return "cloud operation completed with failure" }
func (e *OperationFailure) Definitive() bool { return true }

// Client wraps the GCP IAM and Cloud Resource Manager APIs.
type Client struct {
	ProjectID  string
	iamService *iam.Service
	crmService *cloudresourcemanager.Service
}

// NewClient creates a new IAM client using Application Default Credentials.
func NewClient(ctx context.Context, projectID string, opts ...option.ClientOption) (*Client, error) {
	opts = append([]option.ClientOption{option.WithScopes(iam.CloudPlatformScope)}, opts...)
	iamService, err := iam.NewService(ctx, opts...)
	if err != nil {
		return nil, wrapAuthError("creating IAM client", err)
	}
	crmService, err := cloudresourcemanager.NewService(ctx, opts...)
	if err != nil {
		return nil, wrapAuthError("creating Cloud Resource Manager client", err)
	}
	return &Client{
		ProjectID:  projectID,
		iamService: iamService,
		crmService: crmService,
	}, nil
}

// ============================================================================
// Project
// ============================================================================

// GetProjectNumber retrieves the numeric project number for the configured project.
func (c *Client) GetProjectNumber(ctx context.Context) (int64, error) {
	project, err := c.crmService.Projects.Get(c.ProjectID).Context(ctx).Do()
	if err != nil {
		return 0, wrapAuthError("getting project number", err)
	}
	return project.ProjectNumber, nil
}

// GetProjectIAMPolicy retrieves the IAM policy for the configured project.
func (c *Client) GetProjectIAMPolicy(ctx context.Context) (*cloudresourcemanager.Policy, error) {
	policy, err := c.crmService.Projects.GetIamPolicy(c.ProjectID, &cloudresourcemanager.GetIamPolicyRequest{Options: &cloudresourcemanager.GetPolicyOptions{RequestedPolicyVersion: 3}}).Context(ctx).Do()
	if err != nil {
		return nil, wrapAuthError("getting project IAM policy", err)
	}
	return policy, nil
}

// SetProjectIAMPolicy sets the IAM policy for the configured project.
func (c *Client) SetProjectIAMPolicy(ctx context.Context, policy *cloudresourcemanager.Policy) error {
	_, err := c.crmService.Projects.SetIamPolicy(c.ProjectID, &cloudresourcemanager.SetIamPolicyRequest{
		Policy: policy,
	}).Context(ctx).Do()
	if err != nil {
		return wrapAuthError("setting project IAM policy", err)
	}
	return nil
}

// ============================================================================
// Workload Identity Pools
// ============================================================================

// CreateWorkloadIdentityPool creates a pool and waits for the operation to complete.
func (c *Client) CreateWorkloadIdentityPool(ctx context.Context, parent, poolID string, pool *iam.WorkloadIdentityPool) error {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.
		Create(parent, pool).
		WorkloadIdentityPoolId(poolID).
		Context(ctx).Do()
	if err != nil {
		return err
	}
	return c.waitOperation(ctx, op.Name)
}

// GetWorkloadIdentityPool retrieves a Workload Identity Pool.
func (c *Client) GetWorkloadIdentityPool(ctx context.Context, resource string) (*iam.WorkloadIdentityPool, error) {
	return c.iamService.Projects.Locations.WorkloadIdentityPools.Get(resource).Context(ctx).Do()
}

// DeleteWorkloadIdentityPool deletes a pool and waits for the operation to complete.
func (c *Client) DeleteWorkloadIdentityPool(ctx context.Context, resource string) error {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Delete(resource).Context(ctx).Do()
	if err != nil {
		return err
	}
	return c.waitOperation(ctx, op.Name)
}

// UndeleteWorkloadIdentityPool restores a soft-deleted pool.
func (c *Client) UndeleteWorkloadIdentityPool(ctx context.Context, resource string) error {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Undelete(
		resource, &iam.UndeleteWorkloadIdentityPoolRequest{},
	).Context(ctx).Do()
	if err != nil {
		return err
	}
	return c.waitOperation(ctx, op.Name)
}

// PatchWorkloadIdentityPool updates specific fields on a pool.
func (c *Client) PatchWorkloadIdentityPool(ctx context.Context, resource string, pool *iam.WorkloadIdentityPool, updateMask string) error {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.
		Patch(resource, pool).
		UpdateMask(updateMask).
		Context(ctx).Do()
	if err != nil {
		return err
	}
	return c.waitOperation(ctx, op.Name)
}

// ============================================================================
// OIDC Providers
// ============================================================================

// CreateWorkloadIdentityProvider creates an OIDC provider and waits for the operation.
func (c *Client) CreateWorkloadIdentityProvider(ctx context.Context, parent, providerID string, provider *iam.WorkloadIdentityPoolProvider) error {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Providers.
		Create(parent, provider).
		WorkloadIdentityPoolProviderId(providerID).
		Context(ctx).Do()
	if err != nil {
		return err
	}
	return c.waitOperation(ctx, op.Name)
}

// GetWorkloadIdentityProvider retrieves an OIDC provider.
func (c *Client) GetWorkloadIdentityProvider(ctx context.Context, resource string) (*iam.WorkloadIdentityPoolProvider, error) {
	return c.iamService.Projects.Locations.WorkloadIdentityPools.Providers.Get(resource).Context(ctx).Do()
}

// DeleteWorkloadIdentityProvider deletes an OIDC provider and waits for the operation.
func (c *Client) DeleteWorkloadIdentityProvider(ctx context.Context, resource string) error {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Providers.Delete(resource).Context(ctx).Do()
	if err != nil {
		return err
	}
	return c.waitOperation(ctx, op.Name)
}

// UpdateWorkloadIdentityProvider updates specific fields on a provider.
func (c *Client) UpdateWorkloadIdentityProvider(ctx context.Context, resource string, provider *iam.WorkloadIdentityPoolProvider, updateMask string) error {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Providers.
		Patch(resource, provider).
		UpdateMask(updateMask).
		Context(ctx).Do()
	if err != nil {
		return err
	}
	return c.waitOperation(ctx, op.Name)
}

// UndeleteWorkloadIdentityProvider restores a soft-deleted provider.
func (c *Client) UndeleteWorkloadIdentityProvider(ctx context.Context, resource string) error {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Providers.Undelete(
		resource, &iam.UndeleteWorkloadIdentityPoolProviderRequest{},
	).Context(ctx).Do()
	if err != nil {
		return err
	}
	return c.waitOperation(ctx, op.Name)
}

// ============================================================================
// Service Accounts
// ============================================================================

// CreateServiceAccount creates a Google Service Account.
func (c *Client) CreateServiceAccount(ctx context.Context, accountID string, sa *iam.ServiceAccount) (*iam.ServiceAccount, error) {
	return c.iamService.Projects.ServiceAccounts.Create(
		fmt.Sprintf("projects/%s", c.ProjectID),
		&iam.CreateServiceAccountRequest{AccountId: accountID, ServiceAccount: sa},
	).Context(ctx).Do()
}

// DeleteServiceAccount deletes a Google Service Account by resource name.
func (c *Client) DeleteServiceAccount(ctx context.Context, resource string) error {
	_, err := c.iamService.Projects.ServiceAccounts.Delete(resource).Context(ctx).Do()
	return err
}

// GetServiceAccountIAMPolicy retrieves the IAM policy for a service account.
func (c *Client) GetServiceAccountIAMPolicy(ctx context.Context, resource string) (*iam.Policy, error) {
	return c.iamService.Projects.ServiceAccounts.GetIamPolicy(resource).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
}

// SetServiceAccountIAMPolicy sets the IAM policy for a service account.
func (c *Client) SetServiceAccountIAMPolicy(ctx context.Context, resource string, policy *iam.Policy) error {
	_, err := c.iamService.Projects.ServiceAccounts.SetIamPolicy(
		resource, &iam.SetIamPolicyRequest{Policy: policy},
	).Context(ctx).Do()
	return err
}

// ============================================================================
// Compound IAM Operations
// ============================================================================

// AddProjectIAMRoles adds a member to one or more role bindings in the project IAM policy.
// It performs get-modify-set atomically. Returns the list of roles that were actually added
// (excludes roles the member already had).
func (c *Client) AddProjectIAMRoles(ctx context.Context, member string, roles []string) ([]string, error) {
	policy, err := c.GetProjectIAMPolicy(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting project IAM policy: %w", err)
	}

	var added []string
	for _, role := range roles {
		if AddProjectPolicyMember(policy, role, member) {
			added = append(added, role)
		}
	}

	if len(added) > 0 {
		if err := c.SetProjectIAMPolicy(ctx, policy); err != nil {
			return nil, fmt.Errorf("setting project IAM policy: %w", err)
		}
	}
	return added, nil
}

// RemoveProjectIAMRoles removes a member from one or more role bindings in the project IAM policy.
// It performs get-modify-set atomically. Returns the list of roles that were actually removed
// (excludes roles the member didn't have).
func (c *Client) RemoveProjectIAMRoles(ctx context.Context, member string, roles []string) ([]string, error) {
	policy, err := c.GetProjectIAMPolicy(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting project IAM policy: %w", err)
	}

	var removed []string
	for _, role := range roles {
		if RemoveProjectPolicyMember(policy, role, member) {
			removed = append(removed, role)
		}
	}

	if len(removed) > 0 {
		if err := c.SetProjectIAMPolicy(ctx, policy); err != nil {
			return nil, fmt.Errorf("setting project IAM policy: %w", err)
		}
	}
	return removed, nil
}

// AddServiceAccountIAMRoles adds a member to one or more role bindings on a service account.
// It performs get-modify-set atomically. Returns the list of roles that were actually added.
func (c *Client) AddServiceAccountIAMRoles(ctx context.Context, resource, member string, roles []string) ([]string, error) {
	policy, err := c.GetServiceAccountIAMPolicy(ctx, resource)
	if err != nil {
		return nil, fmt.Errorf("getting service account IAM policy: %w", err)
	}

	var added []string
	for _, role := range roles {
		if AddServiceAccountPolicyMember(policy, role, member) {
			added = append(added, role)
		}
	}

	if len(added) > 0 {
		if err := c.SetServiceAccountIAMPolicy(ctx, resource, policy); err != nil {
			return nil, fmt.Errorf("setting service account IAM policy: %w", err)
		}
	}
	return added, nil
}

// ============================================================================
// Policy Helpers
// ============================================================================

// AddProjectPolicyMember adds a member to a role binding in a CRM project policy.
// Returns true if the member was added, false if it already existed.
func AddProjectPolicyMember(policy *cloudresourcemanager.Policy, role, member string) bool {
	for _, binding := range policy.Bindings {
		if binding.Role == role && binding.Condition == nil {
			for _, existing := range binding.Members {
				if existing == member {
					return false
				}
			}
			binding.Members = append(binding.Members, member)
			return true
		}
	}
	policy.Bindings = append(policy.Bindings, &cloudresourcemanager.Binding{
		Role:    role,
		Members: []string{member},
	})
	return true
}

// RemoveProjectPolicyMember removes a member from a role binding in a CRM project policy.
// Returns true if the member was removed, false if it was not found.
func RemoveProjectPolicyMember(policy *cloudresourcemanager.Policy, role, member string) bool {
	for _, binding := range policy.Bindings {
		if binding.Role == role && binding.Condition == nil {
			for i, existing := range binding.Members {
				if existing == member {
					binding.Members[i] = binding.Members[len(binding.Members)-1]
					binding.Members = binding.Members[:len(binding.Members)-1]
					return true
				}
			}
		}
	}
	return false
}

// AddServiceAccountPolicyMember adds a member to a role binding in an IAM service account policy.
// Returns true if the member was added, false if it already existed.
func AddServiceAccountPolicyMember(policy *iam.Policy, role, member string) bool {
	for _, binding := range policy.Bindings {
		if binding.Role == role && binding.Condition == nil {
			for _, existing := range binding.Members {
				if existing == member {
					return false
				}
			}
			binding.Members = append(binding.Members, member)
			return true
		}
	}
	policy.Bindings = append(policy.Bindings, &iam.Binding{
		Role:    role,
		Members: []string{member},
	})
	return true
}

// ============================================================================
// Operations
// ============================================================================

func (c *Client) waitOperation(ctx context.Context, opName string) error {
	deadline := time.Now().Add(operationTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("operation canceled: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("operation timed out: %s", opName)
		}
		op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Operations.Get(opName).Context(ctx).Do()
		if err != nil {
			return err
		}
		if op.Done {
			if op.Error != nil {
				return &OperationFailure{Code: op.Error.Code}
			}
			return nil
		}
		select {
		case <-time.After(operationPollInterval):
		case <-ctx.Done():
			return fmt.Errorf("operation canceled during polling: %w", ctx.Err())
		}
	}
}
