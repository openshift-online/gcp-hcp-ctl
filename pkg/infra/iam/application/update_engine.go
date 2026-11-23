package application

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
)

// PreviewLinkedUpdate reads the full retained union after earlier recorded
// updates. No durable state, cloud role or API assertion is changed.
func PreviewLinkedUpdate(ctx context.Context, c *platformapi.Client, cloud CloudReader, base *Journal, state *UpdateJournal, name, version, group string, now time.Time) (*UpdatePreview, error) {
	if state != nil && state.Cleanup != nil {
		return nil, fmt.Errorf("installation teardown has started")
	}
	reader, err := LinkedReader(cloud, base, state)
	if err != nil {
		return nil, err
	}
	if state != nil && len(state.Operations) > 0 && state.Operations[len(state.Operations)-1].Phase != "Recorded" && state.Operations[len(state.Operations)-1].Phase != "Abandoned" {
		return nil, fmt.Errorf("an unfinished IAM update requires explicit --resume")
	}
	p, err := PreviewUpdate(ctx, c, reader, base, name, version, group, now)
	if err != nil {
		return nil, err
	}
	if state != nil && len(state.Operations) > 0 {
		p.RoleAdditions = nil
		for _, desired := range p.Retention.Resources {
			if desired.Kind != "Role" {
				continue
			}
			priorValue, err := priorRole(base, state, len(state.Operations), desired.Name)
			if err != nil {
				return nil, fmt.Errorf("recorded role outcome is missing")
			}
			prior := &priorValue
			var additions []string
			for _, permission := range desired.Permissions {
				if !slices.Contains(prior.Permissions, permission) {
					additions = append(additions, permission)
				}
			}
			if len(additions) > 0 {
				p.RoleAdditions = append(p.RoleAdditions, Resource{Kind: "Role", Name: desired.Name, Permissions: additions})
			}
		}
		p.PlanFingerprint = previewFingerprint(*p)
	}
	return p, nil
}

// PrepareUpdate freezes fresh preview intent, role etags and original ownership
// before any RPC. The caller must hold the shared installation lock.
func PrepareUpdate(ctx context.Context, cloud CloudReader, base *Journal, state *UpdateJournal, p *UpdatePreview, expected string) error {
	if state == nil || p == nil || expected == "" || p.PlanFingerprint != expected || !p.TargetEligible || p.Retention == nil || p.Retention.Target.Eligible() != nil || state.Cleanup != nil {
		return fmt.Errorf("an eligible exact preview, fingerprint and linked journal are required")
	}
	if err := state.validate(base); err != nil {
		return err
	}
	if len(state.Operations) > 0 && state.Operations[len(state.Operations)-1].Phase != "Recorded" && state.Operations[len(state.Operations)-1].Phase != "Abandoned" {
		return fmt.Errorf("unfinished update requires resume, not a replacement operation")
	}
	if previewFingerprint(*p) != p.PlanFingerprint {
		return fmt.Errorf("preview integrity mismatch")
	}
	op := newUpdateOperation(p)
	if op.ID == "" {
		return fmt.Errorf("cannot allocate update operation identity")
	}
	for _, desired := range p.Retention.Resources {
		if desired.Kind != "Role" {
			continue
		}
		prior, err := priorRole(base, state, len(state.Operations), desired.Name)
		if err != nil {
			return err
		}
		actual, err := cloud.Get(ctx, desired)
		if err != nil || !sameResource(prior, *actual) || prior.UID != actual.UID {
			return fmt.Errorf("owned role changed after preview")
		}
		op.Roles[desired.Name] = RoleOutcome{Before: *actual, State: "Planned"}
	}
	state.Operations = append(state.Operations, op)
	return state.validate(base)
}

// UpdateEngine has one bounded cloud writer: role permissions only. It never
// creates resources, changes trust/grants, or requests an upgrade.
type UpdateEngine struct {
	Client *platformapi.Client
	Cloud  CloudReader
	Writer RolePermissionWriter
	Store  UpdateStore
	Now    func() time.Time
}

func (e *UpdateEngine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e *UpdateEngine) Apply(ctx context.Context, base *Journal, state *UpdateJournal) error {
	if e.Client == nil || e.Cloud == nil || e.Writer == nil || e.Store == nil || state == nil || len(state.Operations) == 0 || state.Cleanup != nil {
		return fmt.Errorf("linked journal, API reader and role-only writer are required")
	}
	if err := state.validate(base); err != nil {
		return err
	}
	op := &state.Operations[len(state.Operations)-1]
	if op.Phase == "Abandoned" {
		return fmt.Errorf("abandoned update cannot be resumed")
	}
	// If a record POST/PATCH response was lost, resolve it before any retry.
	if op.Phase == "AssertionIntent" || op.Phase == "Recorded" {
		current, err := e.Client.Clusters().Get(ctx, base.Cluster.Namespace, base.Cluster.Name)
		if err != nil {
			return err
		}
		if err := checkUpdateCluster(current, base); err != nil {
			return err
		}
		if slices.Contains(current.Spec.Platform.GCP.IAM.TemplateApplications, op.Intent.Retention.Target.Reference) {
			if err := VerifyLinked(ctx, e.Cloud, base, state); err != nil {
				return err
			}
			op.Phase = "Recorded"
			return e.Store.SaveUpdate(state)
		}
		if op.Phase == "Recorded" {
			return fmt.Errorf("previously recorded IAM assertion disappeared")
		}
	}
	if _, err := e.recheck(ctx, base, op); err != nil {
		return err
	}
	if err := VerifyLinked(ctx, e.Cloud, base, state); err != nil {
		return err
	}
	var permissions []string
	for _, r := range op.Intent.Retention.Resources {
		permissions = append(permissions, r.Permissions...)
	}
	if err := e.Cloud.CheckPermissions(ctx, permissions); err != nil {
		return cloudError(err)
	}
	// Persist intent before the first role RPC, including all before etags.
	if err := e.Store.SaveUpdate(state); err != nil {
		return err
	}
	for _, desired := range op.Intent.Retention.Resources {
		if desired.Kind != "Role" {
			continue
		}
		if _, err := e.recheck(ctx, base, op); err != nil {
			return err
		}
		step := op.Roles[desired.Name]
		desired.Owner = base.Owner()
		actual, err := e.Cloud.Get(ctx, desired)
		if err != nil {
			return cloudError(err)
		}
		if step.State == "Verified" {
			if !sameResource(*step.After, *actual) || step.After.UID != actual.UID {
				return fmt.Errorf("verified role drifted during update")
			}
			continue
		}
		if step.State == "Intent" && sameResource(desired, *actual) {
			step.State, step.After = "Verified", actual
			op.Roles[desired.Name] = step
			if err := e.Store.SaveUpdate(state); err != nil {
				return err
			}
			continue
		}
		if !sameResource(step.Before, *actual) || step.Before.UID != actual.UID {
			return fmt.Errorf("role changed outside the journaled update")
		}
		if sameResource(desired, *actual) {
			step.State, step.After = "Verified", actual
			op.Roles[desired.Name] = step
			if err := e.Store.SaveUpdate(state); err != nil {
				return err
			}
			continue
		}
		desired.UID = actual.UID
		if err := e.Writer.CheckRoleUpdateAuthority(ctx); err != nil {
			return cloudError(err)
		}
		step.State = "Intent"
		op.Roles[desired.Name], op.Phase = step, "Applying"
		if err := e.Store.SaveUpdate(state); err != nil {
			return err
		}
		if err := e.Writer.UpdateRolePermissions(ctx, desired); err != nil {
			// Even a timeout or conflict leaves durable intent. Resume reads
			// exact before/after content; a 404 never permits recreation.
			return cloudError(err)
		}
		actual, err = e.Cloud.Get(ctx, desired)
		if err != nil || !sameResource(desired, *actual) || actual.UID == "" {
			return fmt.Errorf("role permission readback did not converge; resume exact operation")
		}
		step.State, step.After = "Verified", actual
		op.Roles[desired.Name] = step
		if err := e.Store.SaveUpdate(state); err != nil {
			return err
		}
	}
	if err := VerifyLinked(ctx, e.Cloud, base, state); err != nil {
		return err
	}
	op.Phase = "CloudVerified"
	if err := e.Store.SaveUpdate(state); err != nil {
		return err
	}
	current, err := e.recheck(ctx, base, op)
	if err != nil {
		return err
	}
	ref := op.Intent.Retention.Target.Reference
	if slices.Contains(current.Spec.Platform.GCP.IAM.TemplateApplications, ref) {
		op.Phase = "Recorded"
		return e.Store.SaveUpdate(state)
	}
	if len(current.Spec.Platform.GCP.IAM.TemplateApplications) >= 128 {
		return fmt.Errorf("application history limit reached")
	}
	apps := append(slices.Clone(current.Spec.Platform.GCP.IAM.TemplateApplications), ref)
	op.Phase = "AssertionIntent"
	if err := e.Store.SaveUpdate(state); err != nil {
		return err
	}
	_, err = e.Client.PatchClusterIAMApplications(ctx, current.Namespace, current.Name, string(current.UID), current.ResourceVersion, apps)
	if err != nil {
		return fmt.Errorf("cloud IAM verified but assertion outcome requires readback on resume: %w", err)
	}
	current, err = e.Client.Clusters().Get(ctx, base.Cluster.Namespace, base.Cluster.Name)
	if err != nil || string(current.UID) != base.Cluster.UID || !slices.Contains(current.Spec.Platform.GCP.IAM.TemplateApplications, ref) {
		return fmt.Errorf("assertion readback pending; resume journaled operation")
	}
	op.Phase = "Recorded"
	return e.Store.SaveUpdate(state)
}

func VerifyLinked(ctx context.Context, cloud CloudReader, base *Journal, state *UpdateJournal) error {
	reader, err := LinkedReader(cloud, base, state)
	if err != nil {
		return err
	}
	return (&Engine{Cloud: reader}).Verify(ctx, &base.Plan, base)
}

func (e *UpdateEngine) recheck(ctx context.Context, base *Journal, op *UpdateOperation) (*v1.Cluster, error) {
	if err := CheckRealm(ctx, e.Client, &base.Plan); err != nil {
		return nil, err
	}
	target, err := Resolve(ctx, e.Client, op.Intent.Retention.Target.Version, op.Intent.Retention.Target.ChannelGroup)
	if err != nil || target.Eligible() != nil || target.Reference != op.Intent.Retention.Target.Reference || target.VersionUID != op.Intent.Retention.Target.VersionUID || target.VersionGeneration != op.Intent.Retention.Target.VersionGeneration {
		return nil, fmt.Errorf("target eligibility or pinned identity changed")
	}
	v, err := e.Client.Versions().Get(ctx, target.Version)
	if err != nil || string(v.UID) != target.VersionUID || v.Generation != target.VersionGeneration || op.Intent.PayloadCatalogBinding != digestJSON([]any{string(v.UID), v.Generation}) {
		return nil, fmt.Errorf("target payload changed")
	}
	current, err := e.Client.Clusters().Get(ctx, base.Cluster.Namespace, base.Cluster.Name)
	if err != nil {
		return nil, err
	}
	if err := checkUpdateCluster(current, base); err != nil {
		return nil, err
	}
	if digestJSON(current.Spec) != op.Intent.ClusterSpecDigest {
		return nil, fmt.Errorf("Cluster spec changed; exact update must be reviewed")
	}
	if err := checkRequirements(current.Status.IAMRequirements, string(current.UID), string(current.UID), current.Generation, e.now()); err != nil {
		return nil, err
	}
	refs := make([]v1.WIFConfigTemplateReference, 0, len(op.Intent.Retention.Required))
	for _, r := range op.Intent.Retention.Required {
		refs = append(refs, r.Reference)
	}
	if err := requirementsCovered(current.Status.IAMRequirements, refs); err != nil {
		return nil, err
	}
	pools, err := e.Client.NodePools().List(ctx, current.Namespace)
	if err != nil {
		return nil, err
	}
	if pools.Continue != "" || pools.ResourceVersion == "" || pools.RemainingItemCount != nil && *pools.RemainingItemCount > 0 {
		return nil, fmt.Errorf("incomplete NodePool inventory")
	}
	var tokens []UpdateNodePool
	for _, p := range pools.Items {
		if p.Spec.ClusterID != current.Name {
			continue
		}
		if !p.DeletionTimestamp.IsZero() {
			return nil, fmt.Errorf("NodePool deletion blocks update")
		}
		if err := checkRequirements(p.Status.IAMRequirements, string(p.UID), string(current.UID), p.Generation, e.now()); err != nil {
			return nil, err
		}
		if err := requirementsCovered(p.Status.IAMRequirements, refs); err != nil {
			return nil, err
		}
		tokens = append(tokens, UpdateNodePool{Name: p.Name, UID: string(p.UID), Generation: p.Generation, SpecDigest: digestJSON(p.Spec)})
	}
	slices.SortFunc(tokens, func(a, b UpdateNodePool) int { return compareNames(a.Name, b.Name) })
	want := slices.Clone(op.Intent.NodePools)
	for i := range want {
		want[i].ResourceVersion = ""
	}
	if !reflect.DeepEqual(tokens, want) {
		return nil, fmt.Errorf("NodePool identity or specs changed during update")
	}
	for _, t := range op.Intent.Retention.Required {
		w, err := e.Client.WIFConfigTemplates().Get(ctx, t.Reference.Name)
		if err != nil || w.Name != t.Reference.Name || string(w.UID) != t.Reference.UID {
			return nil, fmt.Errorf("retained template identity changed")
		}
		digest, err := Digest(w.Spec)
		if err != nil || digest != t.Reference.Digest {
			return nil, fmt.Errorf("retained template content changed")
		}
	}
	return current, nil
}

func requirementsCovered(view *v1.IAMRequirementsStatus, refs []v1.WIFConfigTemplateReference) error {
	if view == nil {
		return fmt.Errorf("missing IAM requirements")
	}
	for _, r := range []*v1.IAMReleaseRequirement{view.Dispatched, view.Pending} {
		if r != nil && (r.ReleaseVersion == "" || r.ReleaseImage == "" || r.ChannelGroup == "" || !slices.Contains(refs, r.TemplateRef)) {
			return fmt.Errorf("running or pending contract changed outside reviewed retained requirements")
		}
	}
	return nil
}
