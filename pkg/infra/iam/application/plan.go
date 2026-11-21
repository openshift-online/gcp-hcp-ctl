package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
)

type Resource struct {
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	UID         string            `json:"uid,omitempty"`
	Owner       string            `json:"owner,omitempty"`
	Disabled    bool              `json:"disabled,omitempty"`
	Deleted     bool              `json:"deleted,omitempty"`
	Permissions []string          `json:"permissions,omitempty"`
	Issuer      string            `json:"issuer,omitempty"`
	Audiences   []string          `json:"audiences,omitempty"`
	Mapping     map[string]string `json:"mapping,omitempty"`
	InlineJWKS  string            `json:"inlineJWKS,omitempty"`
	Operation   string            `json:"operation,omitempty"`
	Stage       string            `json:"stage,omitempty"`
	Condition   string            `json:"condition,omitempty"`
}
type Grant struct {
	Scope  string `json:"scope"` // "project" or an exact service-account resource.
	Role   string `json:"role"`
	Member string `json:"member"`
}
type Identity struct {
	APIEndpoint   string `json:"apiEndpoint"`
	Project       string `json:"project"`
	ProjectNumber string `json:"projectNumber"`
	InfraID       string `json:"infraID"`
	Issuer        string `json:"issuer"`
	WorkerEmail   string `json:"workerEmail"`
	WorkerUID     string `json:"workerUID"`
}
type Plan struct {
	Target    Target     `json:"target"`
	Identity  Identity   `json:"identity"`
	Resources []Resource `json:"resources"`
	Grants    []Grant    `json:"grants"`
}

var infraPattern = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,10}[a-z0-9])?$`)
var projectPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
var numberPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var rolePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,31}$`)
var permissionPattern = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-zA-Z][a-zA-Z0-9]*\.[a-zA-Z][a-zA-Z0-9]*$`)
var subjectPattern = regexp.MustCompile(`^system:serviceaccount:[a-z0-9]([-a-z0-9]*[a-z0-9])?:[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
var components = []string{"cloud-controller", "cloud-network", "ctrlplane-op", "gcp-pd-csi", "image-registry", "nodepool-mgmt"}

// Schema v1 does not carry these existing federation execution conventions.
// They are explicit plan/journal inputs, never implied by the template digest.
const oidcAudience = "openshift"
const subjectMapping = "assertion.sub"

// Build has no I/O. It renders only the literal schemaVersion 1 naming grammar.
func Build(target *Target, id Identity) (*Plan, error) {
	endpoint, endpointErr := platformapi.CanonicalEndpoint(id.APIEndpoint)
	if endpointErr != nil {
		return nil, endpointErr
	}
	id.APIEndpoint = endpoint
	if target == nil || target.Template.SchemaVersion != 1 || !validReference(target.Reference) || target.VersionUID == "" || target.VersionGeneration < 1 {
		return nil, fmt.Errorf("a supported pinned IAM contract is required")
	}
	digest, err := Digest(target.Template)
	if err != nil || digest != target.Reference.Digest || target.Reference.UID == "" {
		return nil, fmt.Errorf("IAM contract pin does not match its content")
	}
	if !projectPattern.MatchString(id.Project) || !numberPattern.MatchString(id.ProjectNumber) || !infraPattern.MatchString(id.InfraID) || id.InfraID == "gcp" || strings.HasPrefix(id.InfraID, "gcp-") {
		return nil, fmt.Errorf("invalid installation identity or reserved federation prefix")
	}
	u, err := url.Parse(id.Issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, "/"+id.InfraID) {
		return nil, fmt.Errorf("issuer must be an HTTPS installation URL ending in the infra ID")
	}
	if id.WorkerEmail != id.ProjectNumber+"-compute@developer.gserviceaccount.com" || id.WorkerUID == "" {
		return nil, fmt.Errorf("actual enabled default Compute service-account identity is required")
	}
	n := target.Template.IAM.Naming
	if n.CustomRoleIDTemplate != "hcp_{infraIDHex}_{role}" || n.WorkloadIdentityPoolIDTemplate != "{infraID}-wi-pool" || n.WorkloadIdentityProviderIDTemplate != "{infraID}-k8s-provider" || len(id.InfraID) > int(n.MaxInfraIDLength) || n.MaxInfraIDLength > 12 || n.MaxCustomRoleIDLength > 64 || n.MaxServiceAccountIDLength > 30 || n.MaxWorkloadIdentityIDLength > 32 {
		return nil, fmt.Errorf("unsupported contract naming or identifier bounds")
	}
	i := target.Template.IAM
	required := slices.Clone(i.RequiredComponents)
	sort.Strings(required)
	if !slices.Equal(required, components) || len(i.ServiceAccounts) != 6 {
		return nil, fmt.Errorf("contract must define the six supported component identities")
	}
	p := &Plan{Target: *target, Identity: id}
	p.Target.Template = *target.Template.DeepCopy()
	accounts := map[string]string{}
	roles := map[string]string{}
	used := map[string]bool{}
	for _, sa := range i.ServiceAccounts {
		if !slices.Contains(components, sa.Name) || accounts[sa.Name] != "" || sa.AccountIDTemplate != "{infraID}-"+sa.Name {
			return nil, fmt.Errorf("invalid component account definition")
		}
		account := id.InfraID + "-" + sa.Name
		if len(account) > int(n.MaxServiceAccountIDLength) {
			return nil, fmt.Errorf("rendered service account exceeds contract bounds")
		}
		email := account + "@" + id.Project + ".iam.gserviceaccount.com"
		accounts[sa.Name] = email
		p.Resources = append(p.Resources, Resource{Kind: "ServiceAccount", Name: "projects/" + id.Project + "/serviceAccounts/" + email})
	}
	poolID := id.InfraID + "-wi-pool"
	providerID := id.InfraID + "-k8s-provider"
	if len(poolID) > int(n.MaxWorkloadIdentityIDLength) || len(providerID) > int(n.MaxWorkloadIdentityIDLength) {
		return nil, fmt.Errorf("rendered federation ID exceeds contract bounds")
	}
	pool := "projects/" + id.ProjectNumber + "/locations/global/workloadIdentityPools/" + poolID
	p.Resources = append(p.Resources, Resource{Kind: "Pool", Name: pool}, Resource{Kind: "Provider", Name: pool + "/providers/" + providerID, Issuer: id.Issuer, Audiences: []string{oidcAudience}, Mapping: map[string]string{"google.subject": subjectMapping}})
	if len(i.Roles) == 0 || len(i.Roles) > 32 {
		return nil, fmt.Errorf("invalid custom-role definitions")
	}
	for _, r := range i.Roles {
		if !rolePattern.MatchString(r.Name) || roles[r.Name] != "" || len(r.Permissions) == 0 || len(r.Permissions) > 512 {
			return nil, fmt.Errorf("invalid custom-role definition")
		}
		rid := "hcp_" + hex.EncodeToString([]byte(id.InfraID)) + "_" + r.Name
		if len(rid) > int(n.MaxCustomRoleIDLength) {
			return nil, fmt.Errorf("rendered custom role exceeds contract bounds")
		}
		permissions := slices.Clone(r.Permissions)
		sort.Strings(permissions)
		for j, perm := range permissions {
			if !permissionPattern.MatchString(perm) || len(perm) > 128 || (j > 0 && permissions[j-1] == perm) {
				return nil, fmt.Errorf("invalid custom-role permission set")
			}
		}
		roles[r.Name] = "projects/" + id.Project + "/roles/" + rid
		p.Resources = append(p.Resources, Resource{Kind: "Role", Name: roles[r.Name], Permissions: permissions, Stage: "GA"})
	}
	seen := map[string]bool{}
	bound := map[string]bool{}
	for _, g := range i.RoleBindings {
		key := g.ServiceAccountRef + "/" + g.RoleRef
		if accounts[g.ServiceAccountRef] == "" || roles[g.RoleRef] == "" || g.Scope.Kind != "Project" || seen[key] {
			return nil, fmt.Errorf("invalid project role grant")
		}
		seen[key] = true
		bound[g.ServiceAccountRef] = true
		used[g.RoleRef] = true
		p.Grants = append(p.Grants, Grant{Scope: "project", Role: roles[g.RoleRef], Member: "serviceAccount:" + accounts[g.ServiceAccountRef]})
	}
	if len(bound) != 6 || len(used) != len(roles) {
		return nil, fmt.Errorf("project grants do not cover the contract")
	}
	federated := slices.Clone(i.WorkloadIdentityBindings.ServiceAccountRefs)
	sort.Strings(federated)
	if i.WorkloadIdentityBindings.Role != "roles/iam.workloadIdentityUser" || !slices.Equal(federated, components) {
		return nil, fmt.Errorf("unsupported federation grant")
	}
	for _, sa := range i.ServiceAccounts {
		if len(sa.WorkloadIdentitySubjects) == 0 || len(sa.WorkloadIdentitySubjects) > 16 {
			return nil, fmt.Errorf("invalid guest subject set")
		}
		seenSubjects := map[string]bool{}
		for _, subject := range sa.WorkloadIdentitySubjects {
			if len(subject) > 256 || !subjectPattern.MatchString(subject) || seenSubjects[subject] {
				return nil, fmt.Errorf("guest federation requires unique exact subjects")
			}
			seenSubjects[subject] = true
			p.Grants = append(p.Grants, Grant{Scope: "projects/" + id.Project + "/serviceAccounts/" + accounts[sa.Name], Role: i.WorkloadIdentityBindings.Role, Member: "principal://iam.googleapis.com/" + pool + "/subject/" + subject})
		}
	}
	if len(i.ServiceAccountRoleBindings) != 1 {
		return nil, fmt.Errorf("one exact worker actAs grant is required")
	}
	g := i.ServiceAccountRoleBindings[0]
	members := slices.Clone(g.MemberServiceAccountRefs)
	sort.Strings(members)
	if g.Role != "roles/iam.serviceAccountUser" || g.Scope.Kind != "ServiceAccount" || g.Scope.Reference != "WorkerServiceAccount" || !slices.Equal(members, []string{"gcp-pd-csi", "nodepool-mgmt"}) {
		return nil, fmt.Errorf("unsupported worker actAs scope or members")
	}
	for _, member := range members {
		p.Grants = append(p.Grants, Grant{Scope: "projects/" + id.Project + "/serviceAccounts/" + id.WorkerEmail, Role: g.Role, Member: "serviceAccount:" + accounts[member]})
	}
	rank := map[string]int{"Role": 0, "ServiceAccount": 1, "Pool": 2, "Provider": 3}
	sort.Slice(p.Resources, func(a, b int) bool {
		x, y := p.Resources[a], p.Resources[b]
		if rank[x.Kind] != rank[y.Kind] {
			return rank[x.Kind] < rank[y.Kind]
		}
		return x.Name < y.Name
	})
	sort.Slice(p.Grants, func(a, b int) bool { return grantKey(p.Grants[a]) < grantKey(p.Grants[b]) })
	return p, nil
}

func grantKey(g Grant) string { return g.Scope + "\x00" + g.Role + "\x00" + g.Member }
func (p *Plan) Fingerprint() string {
	copy := *p
	copy.Target = p.Target
	copy.Target.Template = canonicalSpec(p.Target.Template)
	copy.Target.Block = ""
	b, _ := json.Marshal(copy)
	return fmt.Sprintf("sha256:%x", sha256.Sum256(b))
}
func (p *Plan) Accounts() map[string]string {
	out := map[string]string{}
	for _, sa := range p.Target.Template.IAM.ServiceAccounts {
		out[sa.Name] = p.Identity.InfraID + "-" + sa.Name + "@" + p.Identity.Project + ".iam.gserviceaccount.com"
	}
	return out
}
