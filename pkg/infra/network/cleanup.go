package network

import (
	"context"
	"fmt"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam/application"
)

type OwnedCleaner interface {
	OwnedCloud
	StartDelete(context.Context, OwnedPlan, string) (string, error)
}

func (c *OwnedGCP) StartDelete(ctx context.Context, p OwnedPlan, kind string) (string, error) {
	return c.Client.StartOwnedDelete(ctx, kind, p.Name(kind))
}

func verifiedBootstrapFirewall(p OwnedPlan, kind string, step *application.Step) bool {
	return kind == "Firewall" && p.Name(kind) != "" && step.State == "Verified" && step.DeleteOperation == "" &&
		step.Resource != nil && step.Resource.UID != "" && step.Resource.Kind == kind && step.Resource.Name == p.Name(kind)
}

// CleanupOwned is used only after the caller has proved hosted teardown and
// absence of cloud dependants. Explicit external networks are never deleted.
// Compute deletes have no numeric-ID CAS: the re-GET narrows, not closes, races.
func CleanupOwned(ctx context.Context, c OwnedCleaner, p OwnedPlan, file interface {
	CurrentJournal() *application.Journal
	Save(*application.Journal) error
}, proofs ...func(context.Context, *application.Journal) error) error {
	j := file.CurrentJournal()
	if len(proofs) != 1 || proofs[0] == nil {
		return fmt.Errorf("independent hosted teardown proof is required")
	}
	proof := proofs[0]
	if err := proof(ctx, j); err != nil {
		return err
	}
	if j.NetworkExternal {
		return nil
	}
	if authority, ok := c.(interface {
		CheckCleanupAuthority(context.Context, OwnedPlan) error
	}); ok {
		if err := authority.CheckCleanupAuthority(ctx, p); err != nil {
			return &application.CloudError{Cause: err}
		}
	}
	if j.NetworkFingerprint == "" {
		return nil
	}
	if j.NetworkFingerprint != p.Fingerprint() {
		return fmt.Errorf("cleanup network binding changed")
	}
	j.Phase = "Cleaning"
	if err := file.Save(j); err != nil {
		return err
	}
	for _, kind := range p.Kinds() {
		step := j.Steps["network/"+kind]
		if step == nil || step.State == "Planned" || step.State == "Deleted" {
			continue
		}
		if step.State == "Failed" {
			if _, err := c.Read(ctx, p, kind, j.Owner()); !missing(err) {
				return fmt.Errorf("failed insert has an unexpected network result")
			}
			continue
		}
		// A pre-5.0 bootstrap rule can be adopted by the CPO and removed during
		// normal hosted teardown. Only an already verified, exact journaled rule
		// qualifies; resolving an uncertain create must not imply this handoff.
		controllerRemovalAllowed := verifiedBootstrapFirewall(p, kind, step)
		if (step.State == "Intent" || step.State == "Unknown") && step.Resource != nil && step.Resource.Operation != "" {
			if err := c.Wait(ctx, kind, step.Resource.Operation); err != nil {
				return &application.CloudError{Cause: err}
			}
			targeter, ok := c.(OperationTargeter)
			if !ok {
				return fmt.Errorf("network operation target proof is required")
			}
			id, err := targeter.TargetID(ctx, p, kind, step.Resource.Operation)
			if err != nil {
				return &application.CloudError{Cause: err}
			}
			step.Resource.UID = id
			step.State = "Verified"
			if err := file.Save(j); err != nil {
				return err
			}
		}
		if step.Resource == nil || step.Resource.UID == "" {
			return fmt.Errorf("uncertain network identity must be resolved before deletion")
		}
		v, err := c.Read(ctx, p, kind, j.Owner())
		if missing(err) && controllerRemovalAllowed {
			step.State = "Deleted"
			if err := file.Save(j); err != nil {
				return err
			}
			continue
		}
		if missing(err) && step.State == "Deleting" {
			continue
		}
		if err != nil {
			return &application.CloudError{Cause: err}
		}
		if v.ID != step.Resource.UID || v.Owner != expectedOwner(p, kind, j.Owner()) || !v.Matches {
			return fmt.Errorf("network cleanup ownership or state changed")
		}
	}
	kinds := p.Kinds()
	for i := len(kinds) - 1; i >= 0; i-- {
		kind := kinds[i]
		step := j.Steps["network/"+kind]
		if step == nil || step.State == "Planned" || step.State == "Failed" || step.State == "Deleted" {
			continue
		}
		if err := proof(ctx, j); err != nil {
			return err
		}
		v, err := c.Read(ctx, p, kind, j.Owner())
		if missing(err) && (step.State == "Deleting" || verifiedBootstrapFirewall(p, kind, step)) {
			step.State = "Deleted"
			if err := file.Save(j); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return &application.CloudError{Cause: err}
		}
		if v.ID != step.Resource.UID || v.Owner != expectedOwner(p, kind, j.Owner()) || !v.Matches {
			return fmt.Errorf("network identity changed before cleanup")
		}
		if step.State != "Deleting" {
			step.State = "Deleting"
			if err := file.Save(j); err != nil {
				return err
			}
			operation, err := c.StartDelete(ctx, p, kind)
			step.DeleteOperation = operation
			if err != nil && application.Definitive(err) {
				step.State = "Verified"
				step.DeleteOperation = ""
			}
			if saveErr := file.Save(j); saveErr != nil {
				return saveErr
			}
			if err != nil {
				return &application.CloudError{Cause: err}
			}
		}
		if step.DeleteOperation == "" {
			return fmt.Errorf("network delete outcome is unknown; no repeated delete was sent")
		}
		if err := c.Wait(ctx, kind, step.DeleteOperation); err != nil {
			if application.TerminalFailure(err) {
				actual, readErr := c.Read(ctx, p, kind, j.Owner())
				if missing(readErr) {
					step.State = "Deleted"
				} else if readErr == nil && actual.ID == step.Resource.UID && actual.Owner == expectedOwner(p, kind, j.Owner()) && actual.Matches {
					step.State = "Verified"
					step.DeleteOperation = ""
				}
				if saveErr := file.Save(j); saveErr != nil {
					return saveErr
				}
			}
			return &application.CloudError{Cause: err}
		}
		if _, err := c.Read(ctx, p, kind, j.Owner()); !missing(err) {
			if err != nil {
				return &application.CloudError{Cause: err}
			}
			return fmt.Errorf("network deletion has not converged")
		}
		step.State = "Deleted"
		if err := file.Save(j); err != nil {
			return err
		}
	}
	return nil
}
