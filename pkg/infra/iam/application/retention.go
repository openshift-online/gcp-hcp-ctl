package application

import (
	"fmt"
	"reflect"
	"slices"
)

// RetentionPreview is intentionally not a Plan. Its union is not the executable
// content of any single pinned template and cannot be passed to the create-only
// engine or journal. A future update executor must bind and recheck all retained
// references and prove that the target is eligible before writing cloud state.
type RetentionPreview struct {
	Target    Target     `json:"target"`
	Required  []Target   `json:"required"`
	Identity  Identity   `json:"identity"`
	Resources []Resource `json:"resources"`
	Grants    []Grant    `json:"grants"`
}

// RetainPermissions combines independently resolved contracts for one existing
// installation. Only custom-role permissions may differ: an unchanged project,
// worker identity, service-account set, WIF trust, role names and exact grant
// topology prevent a permission/grant cross-product. It performs no I/O or cloud
// mutation. All plans must be built from their pinned catalog targets first.
//
// The caller must prove and enumerate every running and pending contract; this
// function does not check target eligibility, infer requirements from version
// order, or authorize cloud writes. Retained running contracts may be unverified.
// Required preserves the caller's order and multiplicity, so a later executor
// must use stable, proven workload identities rather than deduplicating releases.
func RetainPermissions(target *Plan, required ...*Plan) (*RetentionPreview, error) {
	if err := canonicalPlan(target); err != nil {
		return nil, fmt.Errorf("target IAM plan: %w", err)
	}
	if len(required) == 0 {
		return nil, fmt.Errorf("at least one running or pending IAM plan is required")
	}
	cloned := clonePlan(target)
	out := &RetentionPreview{
		Target: cloned.Target, Identity: target.Identity,
		Resources: cloned.Resources, Grants: cloned.Grants,
	}
	for _, plan := range required {
		if err := canonicalPlan(plan); err != nil {
			return nil, fmt.Errorf("retained IAM plan: %w", err)
		}
		if plan.Identity != target.Identity || !slices.Equal(plan.Grants, target.Grants) || len(plan.Resources) != len(target.Resources) {
			return nil, fmt.Errorf("IAM identity or grant topology differs; scope-preserving retention is not possible")
		}
		out.Required = append(out.Required, clonePlan(plan).Target)
		for i := range plan.Resources {
			base := target.Resources[i]
			keep := plan.Resources[i]
			if base.Kind != keep.Kind || (base.Kind != "Role" && len(keep.Permissions) != 0) {
				return nil, fmt.Errorf("IAM resource identity or scope differs")
			}
			base.Permissions = nil
			keep.Permissions = nil
			if !reflect.DeepEqual(base, keep) {
				return nil, fmt.Errorf("IAM resource identity or scope differs")
			}
			if base.Kind == "Role" {
				out.Resources[i].Permissions = append(out.Resources[i].Permissions, plan.Resources[i].Permissions...)
				slices.Sort(out.Resources[i].Permissions)
				out.Resources[i].Permissions = slices.Compact(out.Resources[i].Permissions)
				if len(out.Resources[i].Permissions) > 512 {
					return nil, fmt.Errorf("retained custom role exceeds the supported permission limit")
				}
			}
		}
	}
	return out, nil
}

// Reject altered or synthetic resource/grant lists before calculating a union.
// Build also checks catalog reference/digest integrity and the worker identity.
func canonicalPlan(p *Plan) error {
	if p == nil {
		return fmt.Errorf("a pinned IAM plan is required")
	}
	rebuilt, err := Build(&p.Target, p.Identity)
	if err != nil || !reflect.DeepEqual(p, rebuilt) {
		return fmt.Errorf("IAM plan differs from its pinned contract")
	}
	return nil
}

func clonePlan(p *Plan) *Plan {
	out := *p
	out.Target.Template = *p.Target.Template.DeepCopy()
	out.Resources = make([]Resource, len(p.Resources))
	for i, r := range p.Resources {
		out.Resources[i] = r
		out.Resources[i].Permissions = slices.Clone(r.Permissions)
		out.Resources[i].Audiences = slices.Clone(r.Audiences)
		if r.Mapping != nil {
			out.Resources[i].Mapping = make(map[string]string, len(r.Mapping))
			for key, value := range r.Mapping {
				out.Resources[i].Mapping[key] = value
			}
		}
	}
	out.Grants = slices.Clone(p.Grants)
	return &out
}
