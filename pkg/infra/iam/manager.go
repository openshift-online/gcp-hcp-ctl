package iam

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/go-logr/logr"

	gcpiam "github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/iam"

	iamapi "google.golang.org/api/iam/v1"
)

//go:embed iam-bindings.json
var defaultServiceAccountsJSON []byte

const (
	defaultOIDCAudience      = "openshift"
	workloadIdentityUserRole = "roles/iam.workloadIdentityUser"
)

// ServiceAccountDefinition defines a Google Service Account to be created and its role bindings.
type ServiceAccountDefinition struct {
	Name               string                 `json:"name"`
	DisplayName        string                 `json:"displayName"`
	Description        string                 `json:"description"`
	Roles              []string               `json:"roles"`
	K8sServiceAccounts []K8sServiceAccountRef `json:"k8sServiceAccounts,omitempty"`
}

// K8sServiceAccountRef identifies a Kubernetes ServiceAccount for WIF binding.
type K8sServiceAccountRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// ServiceAccountsConfig is the root structure for the service accounts JSON file.
type ServiceAccountsConfig struct {
	ServiceAccounts []ServiceAccountDefinition `json:"serviceAccounts"`
}

// LoadServiceAccountDefinitions loads and parses the service accounts configuration
// from the embedded JSON file.
func LoadServiceAccountDefinitions() ([]ServiceAccountDefinition, error) {
	var config ServiceAccountsConfig
	if err := json.Unmarshal(defaultServiceAccountsJSON, &config); err != nil {
		return nil, fmt.Errorf("failed to parse service accounts configuration: %w", err)
	}

	if len(config.ServiceAccounts) == 0 {
		return nil, fmt.Errorf("service accounts configuration is empty")
	}

	return config.ServiceAccounts, nil
}

// Manager orchestrates IAM resource lifecycle using the GCP IAM client.
type Manager struct {
	projectID     string
	projectNumber string
	infraID       string
	oidcIssuerURL string
	jwksFile      string

	client *gcpiam.Client
	logger logr.Logger
}

func NewManager(ctx context.Context, projectID string, infraID string, jwksFile string, logger logr.Logger) (*Manager, error) {
	if infraID == "" {
		return nil, fmt.Errorf("infraID is required")
	}
	if projectID == "" {
		return nil, fmt.Errorf("projectID is required")
	}

	client, err := gcpiam.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to create IAM client: %w", err)
	}

	return &Manager{
		projectID: projectID,
		infraID:   infraID,
		jwksFile:  jwksFile,
		client:    client,
		logger:    logger,
	}, nil
}

// SetOIDCIssuerURL sets a custom OIDC issuer URL.
func (m *Manager) SetOIDCIssuerURL(url string) {
	m.oidcIssuerURL = url
}

func (m *Manager) GetProjectNumber(ctx context.Context) (string, error) {
	if m.projectNumber != "" {
		m.logger.V(1).Info("Using existing project number", "projectID", m.projectID, "projectNumber", m.projectNumber)
		return m.projectNumber, nil
	}
	m.logger.V(1).Info("Retrieving project number", "projectID", m.projectID)

	projectNumber, err := m.client.GetProjectNumber(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve project number for %s: %w", m.projectID, err)
	}

	m.logger.V(1).Info("Retrieved project number", "projectNumber", projectNumber)
	m.projectNumber = fmt.Sprintf("%d", projectNumber)
	return m.projectNumber, nil
}

// ============================================================================
// Create Methods
// ============================================================================

func (m *Manager) CreateWorkloadIdentityPool(ctx context.Context) (string, error) {
	poolID := m.formatPoolID()
	m.logger.Info("Creating Workload Identity Pool", "poolID", poolID)

	pool := &iamapi.WorkloadIdentityPool{
		Description: fmt.Sprintf("Workload Identity Pool for HyperShift cluster %s", m.infraID),
		DisplayName: poolID,
		Disabled:    false,
	}
	parent := fmt.Sprintf("projects/%s/locations/global", m.projectID)
	err := m.client.CreateWorkloadIdentityPool(ctx, parent, poolID, pool)
	if err != nil {
		if isAlreadyExistsError(err) {
			m.logger.V(1).Info("Workload Identity Pool already exists, checking state", "poolID", poolID)
			return m.ensurePoolUsable(ctx, parent, poolID)
		}
		return "", fmt.Errorf("failed to create Workload Identity Pool: %w", err)
	}

	m.logger.Info("Created Workload Identity Pool", "poolID", poolID)
	return poolID, nil
}

func (m *Manager) ensurePoolUsable(ctx context.Context, parent, poolID string) (string, error) {
	poolResource := m.formatPoolResource(parent, poolID)

	existingPool, err := m.client.GetWorkloadIdentityPool(ctx, poolResource)
	if err != nil {
		return "", fmt.Errorf("failed to get existing pool: %w", err)
	}

	if existingPool.State == "DELETED" {
		m.logger.Info("Pool is soft-deleted, undeleting", "poolID", poolID)
		if err := m.client.UndeleteWorkloadIdentityPool(ctx, poolResource); err != nil {
			return "", fmt.Errorf("failed to undelete pool: %w", err)
		}
		m.logger.V(1).Info("Undeleted pool", "poolID", poolID)

		existingPool, err = m.client.GetWorkloadIdentityPool(ctx, poolResource)
		if err != nil {
			return "", fmt.Errorf("failed to get pool after undelete: %w", err)
		}
	}

	if existingPool.Disabled {
		m.logger.Info("Pool is disabled, enabling", "poolID", poolID)
		existingPool.Disabled = false
		if err := m.client.PatchWorkloadIdentityPool(ctx, poolResource, existingPool, "disabled"); err != nil {
			return "", fmt.Errorf("failed to enable pool: %w", err)
		}
		m.logger.V(1).Info("Enabled pool", "poolID", poolID)
	}

	m.logger.V(1).Info("Pool is usable", "poolID", poolID)
	return poolID, nil
}

func (m *Manager) CreateOIDCProvider(ctx context.Context) (string, string, error) {
	providerID := m.formatProviderID()
	m.logger.Info("Creating OIDC Provider", "providerID", providerID)

	var (
		jwksJson string
		err      error
	)
	if m.jwksFile != "" {
		jwksJson, err = loadAndValidateJWKS(m.jwksFile)
		if err != nil {
			return "", "", err
		}
		m.logger.V(1).Info("Using inline JWKS for OIDC provider")
	} else {
		m.logger.V(1).Info("No JWKS file provided; GCP will fetch keys from issuer URL")
	}

	issuerURI := m.formatIssuerURI()
	m.logger.V(1).Info("Using OIDC issuer URI", "issuerURI", issuerURI)

	providerAudience := m.formatProviderAudience()
	oidc := &iamapi.Oidc{
		AllowedAudiences: []string{defaultOIDCAudience},
		IssuerUri:        issuerURI,
		JwksJson:         jwksJson,
	}
	if jwksJson == "" {
		oidc.ForceSendFields = []string{"JwksJson"}
	}

	provider := &iamapi.WorkloadIdentityPoolProvider{
		Description: fmt.Sprintf("OIDC Provider for HyperShift cluster %s", m.infraID),
		DisplayName: providerID,
		Disabled:    false,
		Oidc:        oidc,
		AttributeMapping: map[string]string{
			"google.subject": "assertion.sub",
		},
	}
	parent := m.formatPoolParent()
	if err := m.client.CreateWorkloadIdentityProvider(ctx, parent, providerID, provider); err != nil {
		if isAlreadyExistsError(err) {
			m.logger.V(1).Info("OIDC Provider already exists, checking state", "providerID", providerID)
			return m.ensureProviderUsable(ctx, providerID, provider, providerAudience)
		}
		return "", "", fmt.Errorf("failed to create OIDC Provider: %w", err)
	}

	m.logger.Info("Created OIDC Provider", "providerID", providerID)
	return providerID, providerAudience, nil
}

func (m *Manager) ensureProviderUsable(ctx context.Context, providerID string, expectedProvider *iamapi.WorkloadIdentityPoolProvider, providerAudience string) (string, string, error) {
	providerResource := m.formatProviderResource()

	existingProvider, err := m.client.GetWorkloadIdentityProvider(ctx, providerResource)
	if err != nil {
		return "", "", fmt.Errorf("failed to get existing provider: %w", err)
	}

	if existingProvider.State == "DELETED" {
		m.logger.Info("Provider is soft-deleted, undeleting", "providerID", providerID)
		if err := m.client.UndeleteWorkloadIdentityProvider(ctx, providerResource); err != nil {
			return "", "", fmt.Errorf("failed to undelete provider: %w", err)
		}
		m.logger.V(1).Info("Undeleted provider", "providerID", providerID)

		existingProvider, err = m.client.GetWorkloadIdentityProvider(ctx, providerResource)
		if err != nil {
			return "", "", fmt.Errorf("failed to get provider after undelete: %w", err)
		}
	}

	disabledMismatch := existingProvider.Disabled

	var issuerMismatch, jwksMismatch bool
	if existingProvider.Oidc == nil || expectedProvider.Oidc == nil {
		m.logger.V(1).Info("Provider has nil OIDC config, treating as mismatch",
			"providerID", providerID,
			"existingOidcNil", existingProvider.Oidc == nil,
			"expectedOidcNil", expectedProvider.Oidc == nil)
		issuerMismatch = true
		jwksMismatch = true
	} else {
		issuerMismatch = existingProvider.Oidc.IssuerUri != expectedProvider.Oidc.IssuerUri
		jwksMismatch = !m.compareJWKS(existingProvider.Oidc.JwksJson, expectedProvider.Oidc.JwksJson)
	}

	needsUpdate := disabledMismatch || issuerMismatch || jwksMismatch

	if needsUpdate {
		m.logger.Info("Updating provider configuration", "providerID", providerID)
		m.logger.V(1).Info("Provider mismatch details",
			"disabledMismatch", disabledMismatch,
			"issuerMismatch", issuerMismatch,
			"jwksMismatch", jwksMismatch)

		expectedProvider.Name = providerResource
		expectedProvider.Disabled = false
		if err := m.client.UpdateWorkloadIdentityProvider(ctx, providerResource, expectedProvider, "disabled,oidc.jwks_json,oidc.issuer_uri"); err != nil {
			return "", "", fmt.Errorf("failed to update provider: %w", err)
		}
		m.logger.V(1).Info("Updated provider", "providerID", providerID)
	} else {
		m.logger.V(1).Info("Provider is already usable", "providerID", providerID)
	}

	return providerID, providerAudience, nil
}

// CreateServiceAccounts creates all Google Service Accounts defined in the template,
// assigns their roles, and creates WIF bindings.
func (m *Manager) CreateServiceAccounts(ctx context.Context) (map[string]string, error) {
	serviceAccountEmails := make(map[string]string)

	definitions, err := LoadServiceAccountDefinitions()
	if err != nil {
		return nil, fmt.Errorf("failed to load service account definitions: %w", err)
	}

	for _, def := range definitions {
		m.logger.V(1).Info("Processing service account", "name", def.Name)

		var email string
		err = retryWithBackoff(ctx, m.logger, fmt.Sprintf("createServiceAccount-%s", def.Name), func() error {
			var createErr error
			email, createErr = m.createServiceAccount(ctx, def)
			return createErr
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create service account %s: %w", def.Name, err)
		}
		serviceAccountEmails[def.Name] = email

		if len(def.Roles) > 0 {
			err := retryWithBackoff(ctx, m.logger, fmt.Sprintf("assignRoles-%s", def.Name), func() error {
				return m.assignRoles(ctx, email, def.Roles)
			})
			if err != nil {
				return nil, fmt.Errorf("failed to assign roles to %s: %w", def.Name, err)
			}
		}

		for i, k8sSA := range def.K8sServiceAccounts {
			err := retryWithBackoff(ctx, m.logger, fmt.Sprintf("createWIFBinding-%s-%d", def.Name, i), func() error {
				return m.createWorkloadIdentityBinding(ctx, email, &k8sSA)
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create WIF binding for %s: %w", def.Name, err)
			}
		}

		m.logger.Info("Configured service account", "name", def.Name, "email", email)
	}

	return serviceAccountEmails, nil
}

func (m *Manager) createServiceAccount(ctx context.Context, def ServiceAccountDefinition) (string, error) {
	accountID := m.formatServiceAccountID(def.Name)
	email := m.formatServiceAccountEmail(def.Name)

	m.logger.V(1).Info("Creating service account", "accountID", accountID)

	sa := &iamapi.ServiceAccount{
		DisplayName: def.DisplayName,
		Description: def.Description,
	}

	_, err := m.client.CreateServiceAccount(ctx, accountID, sa)
	if err != nil {
		if isAlreadyExistsError(err) {
			m.logger.V(1).Info("Service account already exists", "email", email)
			return email, nil
		}
		return "", err
	}

	m.logger.V(1).Info("Created service account", "email", email)
	return email, nil
}

func (m *Manager) assignRoles(ctx context.Context, serviceAccountEmail string, roles []string) error {
	if len(roles) == 0 {
		return nil
	}

	member := m.formatServiceAccountMember(serviceAccountEmail)
	m.logger.V(1).Info("Assigning project IAM roles", "member", member, "roles", roles)

	added, err := m.client.AddProjectIAMRoles(ctx, member, roles)
	if err != nil {
		return fmt.Errorf("failed to assign roles: %w", err)
	}

	if len(added) > 0 {
		m.logger.V(1).Info("Added project IAM role bindings", "member", member, "added", added)
	} else {
		m.logger.V(1).Info("All role bindings already exist", "member", member)
	}
	return nil
}

func (m *Manager) createWorkloadIdentityBinding(ctx context.Context, serviceAccountEmail string, k8sSA *K8sServiceAccountRef) error {
	member := m.formatWIFPrincipal(k8sSA.Namespace, k8sSA.Name)
	resource := m.formatServiceAccountResource(serviceAccountEmail)
	k8sSAName := fmt.Sprintf("%s/%s", k8sSA.Namespace, k8sSA.Name)

	m.logger.V(1).Info("Checking WIF binding", "k8sSA", k8sSAName, "gsaEmail", serviceAccountEmail)

	added, err := m.client.AddServiceAccountIAMRoles(ctx, resource, member, []string{workloadIdentityUserRole})
	if err != nil {
		return fmt.Errorf("failed to create WIF binding: %w", err)
	}

	if len(added) > 0 {
		m.logger.V(1).Info("Created WIF binding", "k8sSA", k8sSAName, "gsaEmail", serviceAccountEmail)
	} else {
		m.logger.V(1).Info("WIF binding already exists", "k8sSA", k8sSAName, "gsaEmail", serviceAccountEmail)
	}
	return nil
}

// ============================================================================
// Destroy Methods
// ============================================================================

// DeleteWorkloadIdentityPool deletes the Workload Identity Pool for this cluster.
func (m *Manager) DeleteWorkloadIdentityPool(ctx context.Context) error {
	poolID := m.formatPoolID()
	parent := fmt.Sprintf("projects/%s/locations/global", m.projectID)
	poolResource := m.formatPoolResource(parent, poolID)

	m.logger.Info("Deleting Workload Identity Pool", "poolID", poolID)

	if err := m.client.DeleteWorkloadIdentityPool(ctx, poolResource); err != nil {
		if isNotFoundError(err) {
			m.logger.V(1).Info("Workload Identity Pool not found, skipping", "poolID", poolID)
			return nil
		}
		return fmt.Errorf("failed to delete Workload Identity Pool: %w", err)
	}

	m.logger.Info("Deleted Workload Identity Pool", "poolID", poolID)
	return nil
}

// DeleteOIDCProvider deletes the OIDC Provider for this cluster.
func (m *Manager) DeleteOIDCProvider(ctx context.Context) error {
	providerID := m.formatProviderID()
	providerResource := m.formatProviderResource()

	m.logger.Info("Deleting OIDC Provider", "providerID", providerID)

	if err := m.client.DeleteWorkloadIdentityProvider(ctx, providerResource); err != nil {
		if isNotFoundError(err) {
			m.logger.V(1).Info("OIDC Provider not found, skipping", "providerID", providerID)
			return nil
		}
		return fmt.Errorf("failed to delete OIDC Provider: %w", err)
	}

	m.logger.Info("Deleted OIDC Provider", "providerID", providerID)
	return nil
}

// DeleteServiceAccounts deletes all Google Service Accounts created for this cluster.
func (m *Manager) DeleteServiceAccounts(ctx context.Context) error {
	definitions, err := LoadServiceAccountDefinitions()
	if err != nil {
		return fmt.Errorf("failed to load service account definitions: %w", err)
	}

	var deleteErrors []error

	for _, def := range definitions {
		email := m.formatServiceAccountEmail(def.Name)
		m.logger.V(1).Info("Deleting service account", "name", def.Name, "email", email)

		if len(def.Roles) > 0 {
			if err := m.removeRoles(ctx, email, def.Roles); err != nil {
				m.logger.Error(err, "Failed to remove role bindings, continuing with deletion", "email", email)
			}
		}

		resource := m.formatServiceAccountResource(email)
		if err := m.client.DeleteServiceAccount(ctx, resource); err != nil {
			if !isNotFoundError(err) {
				deleteErrors = append(deleteErrors, fmt.Errorf("failed to delete service account %s: %w", email, err))
				continue
			}
			m.logger.V(1).Info("Service account not found, skipping", "email", email)
		} else {
			m.logger.V(1).Info("Deleted service account", "email", email)
		}
	}

	if len(deleteErrors) > 0 {
		return errors.Join(deleteErrors...)
	}

	return nil
}

func (m *Manager) removeRoles(ctx context.Context, serviceAccountEmail string, roles []string) error {
	if len(roles) == 0 {
		return nil
	}

	member := m.formatServiceAccountMember(serviceAccountEmail)
	m.logger.V(1).Info("Removing project IAM roles", "member", member, "roles", roles)

	removed, err := m.client.RemoveProjectIAMRoles(ctx, member, roles)
	if err != nil {
		return fmt.Errorf("failed to remove roles: %w", err)
	}

	if len(removed) > 0 {
		m.logger.V(1).Info("Removed project IAM role bindings", "member", member, "removed", removed)
	} else {
		m.logger.V(1).Info("No role bindings found to remove", "member", member)
	}
	return nil
}

// ============================================================================
// Formatting Helpers
// ============================================================================

func (m *Manager) formatPoolID() string {
	return fmt.Sprintf("%s-wi-pool", m.infraID)
}

func (m *Manager) formatProviderID() string {
	return fmt.Sprintf("%s-k8s-provider", m.infraID)
}

func (m *Manager) formatProviderAudience() string {
	return fmt.Sprintf("//iam.googleapis.com/projects/%s/locations/global/workloadIdentityPools/%s/providers/%s",
		m.projectNumber, m.formatPoolID(), m.formatProviderID())
}

func (m *Manager) formatIssuerURI() string {
	if m.oidcIssuerURL != "" {
		return m.oidcIssuerURL
	}
	return fmt.Sprintf("https://hypershift-%s-oidc", m.infraID)
}

func (m *Manager) formatServiceAccountID(componentName string) string {
	return fmt.Sprintf("%s-%s", m.infraID, componentName)
}

func (m *Manager) formatServiceAccountEmail(componentName string) string {
	return fmt.Sprintf("%s@%s.iam.gserviceaccount.com", m.formatServiceAccountID(componentName), m.projectID)
}

func (m *Manager) formatServiceAccountResource(serviceAccountEmail string) string {
	return fmt.Sprintf("projects/%s/serviceAccounts/%s", m.projectID, serviceAccountEmail)
}

func (m *Manager) formatServiceAccountMember(serviceAccountEmail string) string {
	return fmt.Sprintf("serviceAccount:%s", serviceAccountEmail)
}

func (m *Manager) formatWIFPrincipal(namespace, serviceAccountName string) string {
	return fmt.Sprintf("principal://iam.googleapis.com/projects/%s/locations/global/workloadIdentityPools/%s/subject/system:serviceaccount:%s:%s",
		m.projectNumber, m.formatPoolID(), namespace, serviceAccountName)
}

func (m *Manager) formatPoolResource(parent, poolID string) string {
	return fmt.Sprintf("%s/workloadIdentityPools/%s", parent, poolID)
}

func (m *Manager) formatPoolParent() string {
	return fmt.Sprintf("projects/%s/locations/global/workloadIdentityPools/%s", m.projectID, m.formatPoolID())
}

func (m *Manager) formatProviderResource() string {
	return fmt.Sprintf("%s/providers/%s", m.formatPoolParent(), m.formatProviderID())
}

// ============================================================================
// JWKS Helpers
// ============================================================================

func loadAndValidateJWKS(filePath string) (string, error) {
	jwksData, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read JWKS file: %w", err)
	}
	var js map[string]any
	if err := json.Unmarshal(jwksData, &js); err != nil {
		return "", fmt.Errorf("JWKS file contains invalid JSON: %w", err)
	}
	return string(jwksData), nil
}

func (m *Manager) compareJWKS(jwks1, jwks2 string) bool {
	jwks1 = strings.TrimSpace(jwks1)
	jwks2 = strings.TrimSpace(jwks2)

	if jwks1 == "" && jwks2 == "" {
		return true
	}
	if jwks1 == "" || jwks2 == "" {
		return false
	}

	var obj1, obj2 map[string]any

	if err := json.Unmarshal([]byte(jwks1), &obj1); err != nil {
		m.logger.V(1).Info("Failed to parse existing JWKS JSON", "error", err)
		return false
	}

	if err := json.Unmarshal([]byte(jwks2), &obj2); err != nil {
		m.logger.V(1).Info("Failed to parse expected JWKS JSON", "error", err)
		return false
	}

	canonical1, err1 := json.Marshal(obj1)
	canonical2, err2 := json.Marshal(obj2)

	if err1 != nil || err2 != nil {
		m.logger.V(1).Info("Failed to marshal JWKS for comparison", "err1", err1, "err2", err2)
		return false
	}

	return string(canonical1) == string(canonical2)
}
