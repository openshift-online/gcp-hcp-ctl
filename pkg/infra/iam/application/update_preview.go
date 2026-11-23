package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
)

const requirementsMaxAge = 15 * time.Minute

var wifDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// UpdatePreview is a read-only, conservative proposal. It is not an executable
// Plan or a cloud attestation. The source journal is never changed by PreviewUpdate.
type UpdatePreview struct {
	PayloadCatalogBinding  string            `json:"payloadCatalogBinding"`
	ClusterSpecDigest      string            `json:"clusterSpecDigest"`
	ClusterUID             string            `json:"clusterUID"`
	ClusterResourceVersion string            `json:"clusterResourceVersion"`
	NodePoolListVersion    string            `json:"nodePoolListVersion"`
	NodePools              []UpdateNodePool  `json:"nodePools"`
	Retention              *RetentionPreview `json:"retention"`
	RoleAdditions          []Resource        `json:"roleAdditions"`
	NewApplication         bool              `json:"newApplication"`
	TargetEligible         bool              `json:"targetEligibleForNewUse"`
	TargetBlock            string            `json:"targetBlock,omitempty"`
	PlanFingerprint        string            `json:"planFingerprint"`
}

type UpdateNodePool struct {
	SpecDigest      string `json:"specDigest"`
	Name            string `json:"name"`
	UID             string `json:"uid"`
	ResourceVersion string `json:"resourceVersion"`
	Generation      int64  `json:"generation"`
}

// PreviewUpdate requires the original protected journal, a complete API
// inventory, and independently verified current IAM. It never opens an update
// state file or authorizes an upgrade; eligibility is reported, not bypassed.
func PreviewUpdate(ctx context.Context, c *platformapi.Client, cloud CloudReader, base *Journal, clusterName, version, group string, now time.Time) (*UpdatePreview, error) {
	if c == nil || cloud == nil || base == nil || base.Cluster.State != "Confirmed" || base.Cluster.UID == "" || base.Phase != "ClusterSubmitted" || base.VerifiedAt == nil || clusterName != base.Cluster.Name || c.Project() != base.Plan.Identity.Project {
		return nil, fmt.Errorf("confirmed owned installation journal and matching Cluster are required")
	}
	if err := base.validate(nil); err != nil {
		return nil, err
	}
	if err := CheckRealm(ctx, c, &base.Plan); err != nil {
		return nil, fmt.Errorf("installation API realm changed: %w", err)
	}
	current, err := c.Clusters().Get(ctx, c.Namespace(), clusterName)
	if err != nil {
		return nil, fmt.Errorf("reading existing Cluster: %w", err)
	}
	if err := checkUpdateCluster(current, base); err != nil {
		return nil, err
	}
	if err := checkRequirements(current.Status.IAMRequirements, string(current.UID), string(current.UID), current.Generation, now); err != nil {
		return nil, fmt.Errorf("control-plane IAM requirements: %w", err)
	}
	list, err := c.NodePools().List(ctx, c.Namespace())
	if err != nil {
		return nil, fmt.Errorf("reading complete NodePool inventory: %w", err)
	}
	if list.Continue != "" || list.ResourceVersion == "" || list.RemainingItemCount != nil && *list.RemainingItemCount > 0 {
		return nil, fmt.Errorf("NodePool inventory is paginated; refusing an incomplete view")
	}
	out := &UpdatePreview{ClusterUID: string(current.UID), ClusterResourceVersion: current.ResourceVersion, NodePoolListVersion: list.ResourceVersion}
	out.ClusterSpecDigest = digestJSON(current.Spec)
	seenPools := make(map[string]bool, len(list.Items))
	for i := range list.Items {
		p := &list.Items[i]
		if p.Name == "" || seenPools[p.Name] {
			return nil, fmt.Errorf("NodePool inventory contains duplicate or unnamed resources")
		}
		seenPools[p.Name] = true
		if p.Spec.ClusterID != clusterName {
			continue
		}
		if p.Namespace != current.Namespace || p.UID == "" || p.ResourceVersion == "" || !p.DeletionTimestamp.IsZero() {
			return nil, fmt.Errorf("NodePool identity or deletion state prevents IAM preparation")
		}
		if err := checkRequirements(p.Status.IAMRequirements, string(p.UID), string(current.UID), p.Generation, now); err != nil {
			return nil, fmt.Errorf("NodePool %q IAM requirements: %w", p.Name, err)
		}
		out.NodePools = append(out.NodePools, UpdateNodePool{Name: p.Name, UID: string(p.UID), ResourceVersion: p.ResourceVersion, Generation: p.Generation, SpecDigest: digestJSON(p.Spec)})
	}
	slices.SortFunc(out.NodePools, func(a, b UpdateNodePool) int { return compareNames(a.Name, b.Name) })
	history := current.Spec.Platform.GCP.IAM.TemplateApplications
	if len(history) == 0 || len(history) > 128 {
		return nil, fmt.Errorf("Cluster has no retained IAM application history")
	}
	seenRefs := make(map[string]bool, len(history))
	for _, ref := range history {
		if !validReference(ref) || !wifDigestPattern.MatchString(ref.Digest) || seenRefs[ref.Name] {
			return nil, fmt.Errorf("Cluster has malformed or duplicate retained IAM references")
		}
		seenRefs[ref.Name] = true
	}
	for _, view := range append([]*v1.IAMRequirementsStatus{current.Status.IAMRequirements}, poolRequirements(list.Items, clusterName)...) {
		for _, requirement := range []*v1.IAMReleaseRequirement{view.Dispatched, view.Pending} {
			if requirement == nil {
				continue
			}
			if requirement.ReleaseVersion == "" || requirement.ReleaseImage == "" || requirement.ChannelGroup == "" || !slices.Contains(history, requirement.TemplateRef) {
				return nil, fmt.Errorf("observed or pending requirement is not covered by retained Cluster history")
			}
		}
	}
	target, err := Resolve(ctx, c, version, group)
	if err != nil {
		return nil, fmt.Errorf("resolving exact update target: %w", err)
	}
	for _, ref := range history {
		if ref.Name == target.Reference.Name && ref != target.Reference {
			return nil, fmt.Errorf("target conflicts with a retained template name")
		}
	}
	v, err := c.Versions().Get(ctx, version)
	if err != nil || string(v.UID) != target.VersionUID || v.Generation != target.VersionGeneration {
		return nil, fmt.Errorf("target release payload is not pinned")
	}
	// ReleaseImage is private. A Version spec edit, including a payload edit,
	// increments generation, so UID+generation binds the hidden payload without
	// adding a second public catalog or claiming registry/image attestation.
	out.PayloadCatalogBinding = digestJSON([]any{target.VersionUID, target.VersionGeneration})
	proposed, err := Build(target, base.Plan.Identity)
	if err != nil {
		return nil, err
	}
	retained := make([]*Plan, 0, len(history))
	for _, ref := range history {
		w, readErr := c.WIFConfigTemplates().Get(ctx, ref.Name)
		if readErr != nil || w.Name != ref.Name || string(w.UID) != ref.UID {
			return nil, fmt.Errorf("retained IAM contract identity is unavailable")
		}
		digest, digestErr := Digest(w.Spec)
		if digestErr != nil || digest != ref.Digest || w.Spec.SchemaVersion != 1 {
			return nil, fmt.Errorf("retained IAM contract content changed")
		}
		// This synthetic Target is *never* eligible for new use. Historic
		// contracts may outlive their Version and may have verified=false.
		legacy := &Target{Version: ref.Name, VersionUID: "history:" + ref.UID, VersionGeneration: 1, ChannelGroup: "history", Reference: ref, Template: w.Spec, Block: "RetainedHistory"}
		p, buildErr := Build(legacy, base.Plan.Identity)
		if buildErr != nil {
			return nil, buildErr
		}
		retained = append(retained, p)
	}
	combined, err := RetainPermissions(proposed, retained...)
	if err != nil {
		return nil, err
	}
	if err := (&Engine{Cloud: cloud}).Verify(ctx, &base.Plan, base); err != nil {
		return nil, fmt.Errorf("original owned IAM readback failed: %w", err)
	}
	var allPermissions []string
	for _, resource := range combined.Resources {
		if resource.Kind == "Role" {
			allPermissions = append(allPermissions, resource.Permissions...)
		}
	}
	if err := cloud.CheckPermissions(ctx, allPermissions); err != nil {
		return nil, fmt.Errorf("target custom-role permissions are not supported: %w", cloudError(err))
	}
	// Cloud readback can take time. Reject an inventory or status update that
	// arrived during the preview instead of publishing a stale fingerprint.
	if err := recheckUpdateSnapshot(ctx, c, current, list); err != nil {
		return nil, err
	}
	latestTarget, err := Resolve(ctx, c, version, group)
	if err != nil || latestTarget.VersionUID != target.VersionUID || latestTarget.VersionGeneration != target.VersionGeneration || latestTarget.Reference != target.Reference || latestTarget.Block != target.Block {
		return nil, fmt.Errorf("target catalog eligibility or identity changed during IAM preview")
	}
	latestVersion, err := c.Versions().Get(ctx, version)
	if err != nil || string(latestVersion.UID) != target.VersionUID || latestVersion.Generation != target.VersionGeneration {
		return nil, fmt.Errorf("target release payload changed during IAM preview")
	}
	out.Retention = combined
	out.NewApplication = !slices.Contains(history, target.Reference)
	out.TargetEligible = target.Eligible() == nil
	out.TargetBlock = target.Block
	if len(base.Plan.Resources) != len(combined.Resources) {
		return nil, fmt.Errorf("owned IAM resource inventory changed")
	}
	for i, desired := range combined.Resources {
		if resourceKey(base.Plan.Resources[i]) != resourceKey(desired) {
			return nil, fmt.Errorf("owned IAM resource identity changed")
		}
		if desired.Kind != "Role" {
			continue
		}
		old := base.Plan.Resources[i]
		var additions []string
		for _, permission := range desired.Permissions {
			if !slices.Contains(old.Permissions, permission) {
				additions = append(additions, permission)
			}
		}
		if len(additions) > 0 {
			out.RoleAdditions = append(out.RoleAdditions, Resource{Kind: "Role", Name: desired.Name, Permissions: additions})
		}
	}
	out.PlanFingerprint = previewFingerprint(*out)
	return out, nil
}

func recheckUpdateSnapshot(ctx context.Context, c *platformapi.Client, original *v1.Cluster, pools *v1.NodePoolList) error {
	fresh, err := c.Clusters().Get(ctx, c.Namespace(), original.Name)
	if err != nil {
		return fmt.Errorf("re-reading existing Cluster: %w", err)
	}
	updated, err := c.NodePools().List(ctx, c.Namespace())
	if err != nil {
		return fmt.Errorf("re-reading NodePool inventory: %w", err)
	}
	if fresh.UID != original.UID || fresh.Generation != original.Generation || !reflect.DeepEqual(fresh.Spec, original.Spec) || !fresh.DeletionTimestamp.IsZero() || updated.Continue != "" || updated.ResourceVersion == "" || updated.RemainingItemCount != nil && *updated.RemainingItemCount > 0 {
		return fmt.Errorf("Cluster or NodePool inventory changed during IAM preview")
	}
	if err := checkRequirements(fresh.Status.IAMRequirements, string(fresh.UID), string(fresh.UID), fresh.Generation, time.Now().UTC()); err != nil {
		return err
	}
	byName := make(map[string]v1.NodePool, len(pools.Items))
	for _, p := range pools.Items {
		if p.Spec.ClusterID != original.Name {
			continue
		}
		if _, found := byName[p.Name]; found {
			return fmt.Errorf("NodePool inventory contains duplicate names")
		}
		byName[p.Name] = p
	}
	for _, p := range updated.Items {
		if p.Spec.ClusterID != original.Name {
			continue
		}
		old, found := byName[p.Name]
		if !found || old.UID != p.UID || old.Generation != p.Generation || !reflect.DeepEqual(old.Spec, p.Spec) || !p.DeletionTimestamp.IsZero() {
			return fmt.Errorf("NodePool inventory changed during IAM preview")
		}
		if err := checkRequirements(p.Status.IAMRequirements, string(p.UID), string(fresh.UID), p.Generation, time.Now().UTC()); err != nil {
			return err
		}
		delete(byName, p.Name)
	}
	if len(byName) > 0 {
		return fmt.Errorf("NodePool disappeared during IAM preview")
	}
	return nil
}

func previewFingerprint(p UpdatePreview) string {
	p.ClusterResourceVersion, p.NodePoolListVersion, p.PlanFingerprint = "", "", ""
	p.NodePools = slices.Clone(p.NodePools)
	for i := range p.NodePools {
		p.NodePools[i].ResourceVersion = ""
	}
	if p.Retention != nil {
		r := *p.Retention
		r.Required = slices.Clone(r.Required)
		for i := range r.Required {
			r.Required[i].Template = canonicalSpec(r.Required[i].Template)
		}
		p.Retention = &r
	}
	return digestJSON(p)
}

func digestJSON(value any) string {
	b, _ := json.Marshal(value)
	return fmt.Sprintf("sha256:%x", sha256.Sum256(b))
}

func compareNames(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func poolRequirements(pools []v1.NodePool, clusterName string) []*v1.IAMRequirementsStatus {
	var out []*v1.IAMRequirementsStatus
	for i := range pools {
		if pools[i].Spec.ClusterID == clusterName {
			out = append(out, pools[i].Status.IAMRequirements)
		}
	}
	return out
}

func checkUpdateCluster(c *v1.Cluster, base *Journal) error {
	if c == nil || c.UID == "" || string(c.UID) != base.Cluster.UID || c.Name != base.Cluster.Name || c.Namespace != base.Cluster.Namespace || c.ResourceVersion == "" || !c.DeletionTimestamp.IsZero() || c.Spec.Platform.Type != "GCP" || c.Spec.Platform.GCP == nil || c.Spec.Platform.GCP.IAM == nil {
		return fmt.Errorf("Cluster identity, deletion state or IAM history differs from owned installation")
	}
	var original v1.Cluster
	if err := json.Unmarshal(base.Cluster.Payload, &original); err != nil || original.Spec.Platform.GCP == nil || original.Spec.Platform.GCP.IAM == nil {
		return fmt.Errorf("original installation payload is unavailable")
	}
	if c.Spec.InfraID != base.Plan.Identity.InfraID || c.Spec.IssuerURL != base.Plan.Identity.Issuer || c.Spec.Platform.GCP.ProjectID != base.Plan.Identity.Project ||
		c.Spec.Platform.GCP.Region != original.Spec.Platform.GCP.Region ||
		c.Spec.Platform.GCP.IAM.WorkerServiceAccount != base.Plan.Identity.WorkerEmail ||
		!reflect.DeepEqual(c.Spec.Platform.GCP.WorkloadIdentity, original.Spec.Platform.GCP.WorkloadIdentity) ||
		c.Spec.Platform.GCP.Network != original.Spec.Platform.GCP.Network || c.Spec.Platform.GCP.Subnet != original.Spec.Platform.GCP.Subnet ||
		!slices.Contains(c.Spec.Platform.GCP.IAM.TemplateApplications, base.Plan.Target.Reference) {
		return fmt.Errorf("Cluster installation identity or original IAM contract changed")
	}
	return nil
}

func checkRequirements(r *v1.IAMRequirementsStatus, objectUID, parentUID string, generation int64, now time.Time) error {
	if r == nil || r.State != "Complete" || r.ObjectUID != objectUID || r.ClusterUID != parentUID || r.ObservedGeneration != generation || r.LastRefreshTime == nil || r.LastRefreshTime.Time.After(now) || now.Sub(r.LastRefreshTime.Time) > requirementsMaxAge || r.Dispatched == nil && r.Pending == nil {
		return fmt.Errorf("complete fresh UID-bound requirements are required")
	}
	return nil
}
