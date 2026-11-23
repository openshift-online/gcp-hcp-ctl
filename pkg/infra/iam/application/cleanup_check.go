package application

import (
	"context"
	"fmt"
)

// CheckCleanup is the read-only counterpart to Cleanup. It neither locks nor
// writes state and never equates an unobserved policy write with no effect.
func (e *Engine) CheckCleanup(ctx context.Context, j *Journal, proof func(context.Context, *Journal) error) error {
	if err := j.validate(nil); err != nil {
		return err
	}
	if proof == nil {
		return fmt.Errorf("independent teardown proof is required")
	}
	if err := proof(ctx, j); err != nil {
		return err
	}
	if authority, ok := e.Cloud.(interface{ CheckCleanupAuthority(context.Context) error }); ok {
		if err := authority.CheckCleanupAuthority(ctx); err != nil {
			return err
		}
	}
	number, err := e.Cloud.ProjectNumber(ctx)
	if err != nil {
		return cloudError(err)
	}
	if number != j.Plan.Identity.ProjectNumber {
		return fmt.Errorf("cleanup project identity changed")
	}
	worker, err := e.Cloud.Get(ctx, Resource{Kind: "ServiceAccount", Name: "projects/" + j.Plan.Identity.Project + "/serviceAccounts/" + j.Plan.Identity.WorkerEmail})
	if err != nil {
		return cloudError(err)
	}
	if worker.UID != j.Plan.Identity.WorkerUID {
		return fmt.Errorf("worker identity changed; its policy must not be edited")
	}
	for _, r := range j.Plan.Resources {
		step := j.Steps[resourceKey(r)]
		if step == nil || step.State == "Planned" || step.State == "Deleted" {
			continue
		}
		r.Owner = j.Owner()
		got, err := e.Cloud.Get(ctx, r)
		if step.State == "Failed" {
			if !notFound(err) {
				return fmt.Errorf("failed creation has an unexpected cloud result")
			}
			continue
		}
		if step.State == "Deleting" && (notFound(err) || err == nil && got.Deleted) {
			continue
		}
		if err != nil {
			return cloudError(err)
		}
		if !sameResource(r, *got) || (step.Resource != nil && step.Resource.UID != "" && step.Resource.UID != got.UID) {
			return fmt.Errorf("resource ownership or identity changed before cleanup")
		}
	}
	for _, g := range j.Plan.Grants {
		step := j.Steps["grant/"+grantKey(g)]
		if step == nil || !step.Added || step.State == "Deleted" {
			continue
		}
		policy, err := e.Cloud.GetPolicy(ctx, g.Scope)
		if err != nil {
			return cloudError(err)
		}
		if (step.State == "Unknown" || step.State == "Intent") && !policyHas(policy, g) {
			return fmt.Errorf("uncertain policy outcome blocks cleanup")
		}
	}
	return nil
}
