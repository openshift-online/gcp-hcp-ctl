// Package application plans and applies one pinned installation IAM contract.
// It contains no embedded permission catalog or legacy IAM fallback.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilversion "k8s.io/apimachinery/pkg/util/version"
)

type Catalog interface {
	Versions() platformapi.VersionInterface
	WIFConfigTemplates() platformapi.WIFConfigTemplateInterface
}

var boundaryName = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func validReference(ref v1.WIFConfigTemplateReference) bool {
	return len(ref.Name) <= 32 && boundaryName.MatchString(ref.Name) && ref.UID != "" && len(ref.UID) <= 128
}

// Target is the complete public identity of the reviewed catalog selection.
type Target struct {
	Version           string                        `json:"version"`
	VersionUID        string                        `json:"versionUID"`
	VersionGeneration int64                         `json:"versionGeneration"`
	ChannelGroup      string                        `json:"channelGroup"`
	Reference         v1.WIFConfigTemplateReference `json:"reference"`
	Template          v1.WIFConfigTemplateSpec      `json:"template"`
	Block             string                        `json:"block,omitempty"`
}

// Resolve follows the server's pin; it never selects a numeric floor itself.
// An intact unverified contract may be inspected, but never authorizes Apply.
func Resolve(ctx context.Context, c Catalog, version, group string) (*Target, error) {
	parsed, err := utilversion.ParseSemantic(version)
	if err != nil || parsed.String() != version || group == "" {
		return nil, fmt.Errorf("an exact canonical release and channel group are required")
	}
	v, err := c.Versions().Get(ctx, version)
	if err != nil {
		return nil, fmt.Errorf("reading target Version: %w", err)
	}
	if v.UID == "" || v.Name != version || !slices.Contains(v.Spec.ChannelGroups, group) || v.Spec.WIFConfigTemplateRef == nil {
		return nil, fmt.Errorf("target Version has no matching retained IAM association")
	}
	ref := *v.Spec.WIFConfigTemplateRef
	if !validReference(ref) {
		return nil, fmt.Errorf("target IAM association is incomplete or malformed")
	}
	w, err := c.WIFConfigTemplates().Get(ctx, ref.Name)
	if err != nil {
		return nil, fmt.Errorf("reading pinned IAM contract: %w", err)
	}
	if w.Spec.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported IAM contract schema version")
	}
	digest, err := Digest(w.Spec)
	if err != nil {
		return nil, err
	}
	if w.Name != ref.Name || string(w.UID) != ref.UID || ref.UID == "" || digest != ref.Digest {
		return nil, fmt.Errorf("pinned IAM contract identity or content does not match")
	}
	target := &Target{Version: version, VersionUID: string(v.UID), VersionGeneration: v.Generation, ChannelGroup: group, Reference: ref, Template: w.Spec}
	cond := meta.FindStatusCondition(v.Status.Conditions, "Available")
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.ObservedGeneration != v.Generation {
		target.Block = "VersionUnavailable"
	}
	if w.Spec.Verified == nil || !*w.Spec.Verified {
		target.Block = "WIFConfigTemplateUnverified"
	}
	return target, nil
}

func (t *Target) Eligible() error {
	if t == nil || t.Block != "" || t.VersionUID == "" || t.VersionGeneration < 1 || t.Template.Verified == nil || !*t.Template.Verified {
		return fmt.Errorf("target is not eligible for new IAM application")
	}
	return nil
}

func Recheck(ctx context.Context, c Catalog, want *Target) error {
	current, err := Resolve(ctx, c, want.Version, want.ChannelGroup)
	if err != nil {
		return err
	}
	if err := current.Eligible(); err != nil {
		return err
	}
	if current.VersionUID != want.VersionUID || current.VersionGeneration != want.VersionGeneration || current.Reference != want.Reference {
		return fmt.Errorf("catalog selection changed; review a fresh plan")
	}
	return nil
}

// Digest implements the documented schemaVersion 1 Go-JSON protocol over public
// types. Golden parity tests prevent drift from the server's canonicalization.
func Digest(spec v1.WIFConfigTemplateSpec) (string, error) {
	data, err := json.Marshal(canonicalSpec(spec))
	if err != nil {
		return "", fmt.Errorf("cannot encode IAM contract")
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}

func canonicalSpec(spec v1.WIFConfigTemplateSpec) v1.WIFConfigTemplateSpec {
	spec = *spec.DeepCopy()
	spec.Verified = nil
	i := &spec.IAM
	sort.Strings(i.RequiredComponents)
	sort.Slice(i.ServiceAccounts, func(a, b int) bool { return i.ServiceAccounts[a].Name < i.ServiceAccounts[b].Name })
	for n := range i.ServiceAccounts {
		sort.Strings(i.ServiceAccounts[n].WorkloadIdentitySubjects)
	}
	sort.Slice(i.Roles, func(a, b int) bool { return i.Roles[a].Name < i.Roles[b].Name })
	for n := range i.Roles {
		sort.Strings(i.Roles[n].Permissions)
	}
	sort.Slice(i.RoleBindings, func(a, b int) bool {
		x, y := i.RoleBindings[a], i.RoleBindings[b]
		return x.ServiceAccountRef+"/"+x.RoleRef < y.ServiceAccountRef+"/"+y.RoleRef
	})
	sort.Strings(i.WorkloadIdentityBindings.ServiceAccountRefs)
	for n := range i.ServiceAccountRoleBindings {
		sort.Strings(i.ServiceAccountRoleBindings[n].MemberServiceAccountRefs)
	}
	return spec
}
