package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
)

type Engine struct {
	// StopAfterFirstRole is a bounded cleanup rehearsal, never IAM coverage.
	StopAfterFirstRole bool
	Cloud              CloudReader
	Store              JournalStore
	Recheck            func(context.Context) error
	Pause              func(context.Context) error
}

var ErrControlledStop = errors.New("operation paused after its first verified unbound role; IAM coverage is not verified")

func (e *Engine) pause(ctx context.Context) error {
	if e.Pause != nil {
		return e.Pause(ctx)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
		return nil
	}
}
func resourceKey(r Resource) string { return r.Kind + "/" + r.Name }
func sameResource(want, got Resource) bool {
	if got.Kind != want.Kind || got.Name != want.Name || got.Owner != want.Owner || got.Disabled || got.Deleted {
		return false
	}
	a, b := slices.Clone(want.Permissions), slices.Clone(got.Permissions)
	sort.Strings(a)
	sort.Strings(b)
	x, y := slices.Clone(want.Audiences), slices.Clone(got.Audiences)
	sort.Strings(x)
	sort.Strings(y)
	return slices.Equal(a, b) && slices.Equal(x, y) && want.Issuer == got.Issuer && want.InlineJWKS == got.InlineJWKS && want.Stage == got.Stage && want.Condition == got.Condition && reflect.DeepEqual(want.Mapping, got.Mapping)
}
func policyHas(p *crm.Policy, g Grant) bool {
	if p == nil {
		return false
	}
	for _, b := range p.Bindings {
		if b.Role == g.Role && b.Condition == nil && slices.Contains(b.Members, g.Member) {
			return true
		}
	}
	return false
}
func clonePolicy(p *crm.Policy) (*crm.Policy, error) {
	if p == nil || p.Etag == "" || p.Version > 3 {
		return nil, fmt.Errorf("policy is missing optimistic concurrency information")
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("cannot encode policy")
	}
	var out crm.Policy
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("cannot copy policy")
	}
	out.Version = 3
	return &out, nil
}

func policyFingerprint(p *crm.Policy) string {
	data, _ := json.Marshal(p)
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}
func editPolicy(p *crm.Policy, g Grant, remove bool) (bool, error) {
	for _, b := range p.Bindings {
		if b.Role == g.Role && b.Condition != nil && slices.Contains(b.Members, g.Member) {
			return false, fmt.Errorf("managed member has unexpected conditional grant")
		}
	}
	if !remove && policyHas(p, g) {
		return false, nil
	}
	for index, b := range p.Bindings {
		if b.Role != g.Role {
			continue
		}
		if b.Condition != nil {
			if slices.Contains(b.Members, g.Member) {
				return false, fmt.Errorf("managed member has unexpected conditional grant")
			}
			continue
		}
		idx := slices.Index(b.Members, g.Member)
		if remove {
			if idx >= 0 {
				b.Members = append(b.Members[:idx], b.Members[idx+1:]...)
				if len(b.Members) == 0 {
					p.Bindings = append(p.Bindings[:index], p.Bindings[index+1:]...)
				}
				return true, nil
			}
			continue
		}
		if idx >= 0 {
			return false, nil
		}
		b.Members = append(b.Members, g.Member)
		return true, nil
	}
	if !remove {
		p.Bindings = append(p.Bindings, &crm.Binding{Role: g.Role, Members: []string{g.Member}})
		return true, nil
	}
	return false, nil
}

// Inspect reads the full desired resource set; it never creates a state file or
// repairs a collision. It is shared by preview and apply preflight.
func (e *Engine) Inspect(ctx context.Context, p *Plan, j *Journal) error {
	if e.Cloud == nil {
		return fmt.Errorf("cloud reader is required")
	}
	checked, err := Build(&p.Target, p.Identity)
	if err != nil || checked.Fingerprint() != p.Fingerprint() {
		return fmt.Errorf("plan integrity mismatch")
	}
	if j != nil {
		if err := j.validate(p); err != nil {
			return err
		}
	}
	number, err := e.Cloud.ProjectNumber(ctx)
	if err != nil {
		return cloudError(err)
	}
	if number != p.Identity.ProjectNumber {
		return fmt.Errorf("project number changed")
	}
	worker, err := e.Cloud.Get(ctx, Resource{Kind: "ServiceAccount", Name: "projects/" + p.Identity.Project + "/serviceAccounts/" + p.Identity.WorkerEmail})
	if err != nil {
		return cloudError(err)
	}
	if worker.UID != p.Identity.WorkerUID || worker.Disabled || worker.Deleted {
		return fmt.Errorf("default worker service-account identity changed")
	}
	var permissions []string
	for _, r := range p.Resources {
		permissions = append(permissions, r.Permissions...)
		got, err := e.Cloud.Get(ctx, r)
		var step *Step
		if j != nil {
			step = j.Steps[resourceKey(r)]
		}
		if notFound(err) {
			if step != nil && step.State != "Planned" && step.State != "Failed" && step.State != "Deleted" {
				return fmt.Errorf("previously attempted resource is absent; outcome or tombstone requires investigation")
			}
			continue
		}
		if err != nil {
			return cloudError(err)
		}
		if j == nil || step == nil || step.State == "Planned" || step.State == "Failed" || step.State == "Deleted" {
			return fmt.Errorf("resource collision; fresh IDs or an exact owned resume are required")
		}
		r.Owner = j.Owner()
		if !sameResource(r, *got) || (step.Resource != nil && step.Resource.UID != "" && step.Resource.UID != got.UID) {
			return fmt.Errorf("owned resource identity, trust or content changed")
		}
	}
	return cloudError(e.Cloud.CheckPermissions(ctx, permissions))
}

func (e *Engine) gate(ctx context.Context, p *Plan) error {
	if err := p.Target.Eligible(); err != nil {
		return err
	}
	if e.Recheck == nil {
		return fmt.Errorf("a fresh catalog recheck is required")
	}
	return e.Recheck(ctx)
}

func (e *Engine) Apply(ctx context.Context, j *Journal) error {
	writer, ok := e.Cloud.(CloudWriter)
	if !ok {
		return fmt.Errorf("cloud mutation interface is not configured")
	}
	p := &j.Plan
	if e.Store == nil {
		return fmt.Errorf("durable journal storage is required")
	}
	if err := j.validate(p); err != nil {
		return err
	}
	if e.StopAfterFirstRole {
		if len(p.Resources) == 0 || p.Resources[0].Kind != "Role" {
			return fmt.Errorf("bounded role rehearsal requires a role-first plan")
		}
		first := resourceKey(p.Resources[0])
		for key, step := range j.Steps {
			if key != first && step.State != "Planned" {
				return fmt.Errorf("bounded role rehearsal cannot resume an operation with other attempted work")
			}
		}
	}
	if j.Phase == "Cleaning" || j.Phase == "Cleaned" {
		return fmt.Errorf("a cleanup operation cannot be reprovisioned")
	}
	if j.Cluster.State != "None" {
		return fmt.Errorf("Cluster submission already started; only submission resolution is permitted")
	}
	if err := e.gate(ctx, p); err != nil {
		return err
	}
	if preflight, ok := e.Cloud.(interface{ CheckWriterAuthority(context.Context) error }); ok {
		if err := preflight.CheckWriterAuthority(ctx); err != nil {
			return err
		}
	}
	// An LRO name is persisted immediately after Start and before Wait. Resume
	// waits that exact operation; it never creates another pool/provider.
	if async, ok := e.Cloud.(AsyncCloud); ok {
		for _, step := range j.Steps {
			if (step.State == "Intent" || step.State == "Unknown") && step.Resource != nil && (step.Resource.Kind == "Pool" || step.Resource.Kind == "Provider") && step.Resource.Operation != "" {
				if err := async.WaitCreate(ctx, step.Resource.Operation); err != nil {
					if terminalOperation(err) {
						if _, readErr := e.Cloud.Get(ctx, *step.Resource); notFound(readErr) {
							step.State = "Failed"
							step.LastFailedOperation = step.Resource.Operation
							step.Resource = nil
							if saveErr := e.Store.Save(j); saveErr != nil {
								return saveErr
							}
						}
					}
					return err
				}
				step.State = "Created"
				if err := e.Store.Save(j); err != nil {
					return err
				}
			}
		}
	}
	if err := e.Inspect(ctx, p, j); err != nil {
		return err
	}
	for _, desired := range p.Resources {
		if err := e.gate(ctx, p); err != nil {
			return err
		}
		desired.Owner = j.Owner()
		key := resourceKey(desired)
		step := j.Steps[key]
		if step == nil {
			step = &Step{State: "Planned"}
			j.Steps[key] = step
		}
		if step.State == "Failed" {
			step.State = "Planned"
			step.Resource = nil
		}
		if step.State == "Planned" {
			if _, err := e.Cloud.Get(ctx, desired); !notFound(err) {
				if err != nil {
					return cloudError(err)
				}
				return fmt.Errorf("resource appeared after preflight; refusing adoption")
			}
			step.State = "Intent"
			step.IntentAt = time.Now().UTC()
			if err := e.Store.Save(j); err != nil {
				return err
			}
			var created *Resource
			var err error
			startedAsync := false
			if async, ok := e.Cloud.(AsyncCloud); ok && (desired.Kind == "Pool" || desired.Kind == "Provider") {
				var operation string
				operation, err = async.StartCreate(ctx, desired)
				startedAsync = err == nil || operation != ""
				created = &Resource{Kind: desired.Kind, Name: desired.Name, Operation: operation}
				step.Resource = created
				if saveErr := e.Store.Save(j); saveErr != nil {
					return saveErr
				}
				if err == nil {
					err = async.WaitCreate(ctx, operation)
				}
			} else {
				created, err = writer.Create(ctx, desired)
			}
			if created != nil {
				step.Resource = created
			}
			if err != nil {
				step.State = "Unknown"
				if Definitive(err) && !startedAsync {
					step.State = "Failed"
					step.Resource = nil
				} else if terminalOperation(err) {
					if _, readErr := e.Cloud.Get(ctx, desired); notFound(readErr) {
						step.State = "Failed"
						if step.Resource != nil {
							step.LastFailedOperation = step.Resource.Operation
						}
						step.Resource = nil
					}
				}
				if saveErr := e.Store.Save(j); saveErr != nil {
					return saveErr
				}
				return cloudError(err)
			}
			step.State = "Created"
			if err := e.Store.Save(j); err != nil {
				return err
			}
		}
		got, err := e.readResource(ctx, desired)
		if err != nil {
			return err
		}
		if !sameResource(desired, *got) || (step.Resource != nil && step.Resource.UID != "" && step.Resource.UID != got.UID) {
			return fmt.Errorf("resource does not match the journaled creation")
		}
		if step.Resource != nil {
			got.Operation = step.Resource.Operation
		}
		step.Resource = got
		step.State = "Verified"
		if err := e.Store.Save(j); err != nil {
			return err
		}
		if e.StopAfterFirstRole {
			j.Phase = "PausedAfterFirstRole"
			if err := e.Store.Save(j); err != nil {
				return err
			}
			return ErrControlledStop
		}
	}
	for _, g := range p.Grants {
		if err := e.gate(ctx, p); err != nil {
			return err
		}
		if err := e.applyGrant(ctx, j, g, false); err != nil {
			return err
		}
	}
	if err := e.Verify(ctx, p, j); err != nil {
		return err
	}
	now := time.Now().UTC()
	j.VerifiedAt = &now
	j.Phase = "IAMVerified"
	return e.Store.Save(j)
}

func (e *Engine) readResource(ctx context.Context, r Resource) (*Resource, error) {
	for attempt := 0; attempt < 30; attempt++ {
		got, err := e.Cloud.Get(ctx, r)
		if err == nil {
			return got, nil
		}
		if !notFound(err) {
			return nil, cloudError(err)
		}
		if err := e.pause(ctx); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("resource propagation did not converge; no success assertion was recorded")
}

func (e *Engine) applyGrant(ctx context.Context, j *Journal, g Grant, remove bool) error {
	key := "grant/" + grantKey(g)
	step := j.Steps[key]
	if remove && (step == nil || !step.Added) {
		return nil
	}
	if step == nil {
		step = &Step{State: "Planned"}
		j.Steps[key] = step
	}
	if step.State == "Failed" {
		step.State = "Planned"
		step.Added = false
	}
	if remove && step.State == "Deleted" {
		return nil
	}
	for attempt := 0; attempt < 30; attempt++ {
		policy, err := e.Cloud.GetPolicy(ctx, g.Scope)
		if err != nil {
			return cloudError(err)
		}
		if step.State == "Planned" {
			if policyHas(policy, g) {
				return fmt.Errorf("desired grant predates this operation; refusing ownership adoption")
			}
			step.BeforePolicyFingerprint = policyFingerprint(policy)
			step.State = "Intent"
			step.IntentAt = time.Now().UTC()
			step.Added = true
			if err := e.Store.Save(j); err != nil {
				return err
			}
		}
		copy, err := clonePolicy(policy)
		if err != nil {
			return err
		}
		changed, err := editPolicy(copy, g, remove)
		if err != nil {
			return err
		}
		if changed {
			step.PolicyEtag = copy.Etag
			if remove {
				step.State = "Deleting"
			}
			if err := e.Store.Save(j); err != nil {
				return err
			}
			writer, ok := e.Cloud.(CloudWriter)
			if !ok {
				return fmt.Errorf("cloud mutation interface is not configured")
			}
			if err := writer.SetPolicy(ctx, g.Scope, copy); err != nil {
				if conflict(err) {
					if err := e.pause(ctx); err != nil {
						return err
					}
					continue
				}
				var api *googleapi.Error
				if errors.As(err, &api) && api.Code == 400 && attempt < 29 {
					if err := e.pause(ctx); err != nil {
						return err
					}
					continue
				}
				step.State = "Unknown"
				if Definitive(err) {
					step.State = "Failed"
				}
				if saveErr := e.Store.Save(j); saveErr != nil {
					return saveErr
				}
				return cloudError(err)
			}
		}
		actual, err := e.Cloud.GetPolicy(ctx, g.Scope)
		if err != nil {
			return cloudError(err)
		}
		if policyHas(actual, g) == remove {
			if err := e.pause(ctx); err != nil {
				return err
			}
			continue
		}
		if remove {
			step.State = "Deleted"
		} else {
			step.State = "Verified"
		}
		step.AfterPolicyFingerprint = policyFingerprint(actual)
		return e.Store.Save(j)
	}
	return fmt.Errorf("policy concurrency or propagation did not converge")
}

// Verify always reads fresh cloud state. Neither a journal timestamp nor a saved
// output file is a substitute for these checks.
func (e *Engine) Verify(ctx context.Context, p *Plan, j *Journal) error {
	if j == nil {
		return fmt.Errorf("owned operation journal is required for verification")
	}
	if err := j.validate(p); err != nil {
		return err
	}
	if err := e.Inspect(ctx, p, j); err != nil {
		return err
	}
	for _, desired := range p.Resources {
		desired.Owner = j.Owner()
		got, err := e.readResource(ctx, desired)
		if err != nil {
			return err
		}
		step := j.Steps[resourceKey(desired)]
		if step == nil || step.Resource == nil || !sameResource(desired, *got) || (step.Resource.UID != "" && step.Resource.UID != got.UID) {
			return fmt.Errorf("independent resource readback does not match ownership and desired contract")
		}
	}
	for _, g := range p.Grants {
		policy, err := e.Cloud.GetPolicy(ctx, g.Scope)
		if err != nil {
			return cloudError(err)
		}
		if !policyHas(policy, g) {
			return fmt.Errorf("required IAM grant is absent from independent readback")
		}
	}
	return nil
}

// Cleanup is intentionally gated by an independent API/dependency proof supplied
// by the command layer. Unknown Cluster submissions never use a 404 shortcut.
func (e *Engine) Cleanup(ctx context.Context, j *Journal, teardownProof func(context.Context, *Journal) error) error {
	if err := e.CheckCleanup(ctx, j, teardownProof); err != nil {
		return err
	}
	writer, ok := e.Cloud.(CloudWriter)
	if !ok {
		return fmt.Errorf("cloud mutation interface is not configured")
	}
	if e.Store == nil || teardownProof == nil {
		return fmt.Errorf("durable state and independent teardown proof are required")
	}
	if err := j.validate(nil); err != nil {
		return err
	}
	if j.Cluster.State == "Intent" || j.Cluster.State == "Unknown" {
		return fmt.Errorf("uncertain Cluster submission blocks IAM cleanup")
	}
	if err := teardownProof(ctx, j); err != nil {
		return err
	}
	number, err := e.Cloud.ProjectNumber(ctx)
	if err != nil {
		return cloudError(err)
	}
	if number != j.Plan.Identity.ProjectNumber {
		return fmt.Errorf("cleanup project mismatch")
	}
	worker, err := e.Cloud.Get(ctx, Resource{Kind: "ServiceAccount", Name: "projects/" + j.Plan.Identity.Project + "/serviceAccounts/" + j.Plan.Identity.WorkerEmail})
	if err != nil {
		return cloudError(err)
	}
	if worker.UID != j.Plan.Identity.WorkerUID {
		return fmt.Errorf("cleanup worker identity changed")
	}
	// Inspect every attempted resource before the first destructive operation.
	for _, desired := range j.Plan.Resources {
		step := j.Steps[resourceKey(desired)]
		if step == nil || step.State == "Planned" || step.State == "Deleted" {
			continue
		}
		if step.State == "Failed" {
			if _, err := e.Cloud.Get(ctx, desired); !notFound(err) {
				return fmt.Errorf("failed creation has an unexpected cloud result")
			}
			continue
		}
		desired.Owner = j.Owner()
		actual, err := e.Cloud.Get(ctx, desired)
		if notFound(err) && step.State == "Deleting" {
			continue
		}
		if err != nil {
			return cloudError(err)
		}
		if step.State == "Intent" || step.State == "Unknown" {
			if !sameResource(desired, *actual) || (step.Resource != nil && step.Resource.UID != "" && step.Resource.UID != actual.UID) {
				return fmt.Errorf("uncertain resource creation blocks cleanup")
			}
			step.Resource = actual
			step.State = "Verified"
			if err := e.Store.Save(j); err != nil {
				return err
			}
		}
		if actual.Deleted && step.State == "Deleting" {
			continue
		}
		if !sameResource(desired, *actual) || step.Resource == nil || (step.Resource.UID != "" && step.Resource.UID != actual.UID) {
			return fmt.Errorf("cleanup ownership or resource identity changed")
		}
	}
	for _, g := range j.Plan.Grants {
		step := j.Steps["grant/"+grantKey(g)]
		if step == nil || step.State != "Intent" && step.State != "Unknown" {
			continue
		}
		policy, err := e.Cloud.GetPolicy(ctx, g.Scope)
		if err != nil {
			return cloudError(err)
		}
		if !policyHas(policy, g) {
			return fmt.Errorf("uncertain policy write has no established outcome; cleanup refused")
		}
		step.State = "Verified"
		if err := e.Store.Save(j); err != nil {
			return err
		}
	}
	j.Phase = "Cleaning"
	if err := e.Store.Save(j); err != nil {
		return err
	}
	for i := len(j.Plan.Grants) - 1; i >= 0; i-- {
		if err := e.applyGrant(ctx, j, j.Plan.Grants[i], true); err != nil {
			return err
		}
	}
	for i := len(j.Plan.Resources) - 1; i >= 0; i-- {
		desired := j.Plan.Resources[i]
		desired.Owner = j.Owner()
		step := j.Steps[resourceKey(desired)]
		if step == nil || step.State == "Planned" || step.State == "Failed" || step.State == "Deleted" {
			continue
		}
		if step.State == "Intent" || step.State == "Unknown" {
			return fmt.Errorf("uncertain resource creation must be resolved before cleanup")
		}
		actual, err := e.Cloud.Get(ctx, desired)
		if notFound(err) {
			if step.State != "Deleting" {
				return fmt.Errorf("owned resource vanished unexpectedly")
			}
			step.State = "Deleted"
			if err := e.Store.Save(j); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return cloudError(err)
		}
		if actual.Deleted && step.State == "Deleting" {
			step.State = "Deleted"
			if err := e.Store.Save(j); err != nil {
				return err
			}
			continue
		}
		if !sameResource(desired, *actual) || step.Resource == nil || (step.Resource.UID != "" && step.Resource.UID != actual.UID) {
			return fmt.Errorf("cleanup ownership or resource identity changed")
		}
		step.State = "Deleting"
		if err := e.Store.Save(j); err != nil {
			return err
		}
		if err := writer.Delete(ctx, *actual); err != nil {
			return cloudError(err)
		}
		gone := false
		for attempt := 0; attempt < 30; attempt++ {
			observed, err := e.Cloud.Get(ctx, desired)
			if notFound(err) {
				gone = true
				break
			}
			if err != nil {
				return cloudError(err)
			}
			if observed.Deleted {
				gone = true
				break
			}
			if err := e.pause(ctx); err != nil {
				return err
			}
		}
		if !gone {
			return fmt.Errorf("deletion has not converged; operation remains journaled")
		}
		step.State = "Deleted"
		if err := e.Store.Save(j); err != nil {
			return err
		}
	}
	j.Phase = "Cleaned"
	return e.Store.Save(j)
}
