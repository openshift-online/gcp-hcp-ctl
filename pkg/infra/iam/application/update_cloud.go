package application

import (
	"context"
	"fmt"

	crm "google.golang.org/api/cloudresourcemanager/v1"
)

// linkedCloud validates the exact role permission/etag lineage before exposing
// the canonical original view to the create-only verifier/cleanup engine. It
// never accepts arbitrary supersets and never modifies the original Plan.
type linkedCloud struct {
	CloudReader
	base  *Journal
	state *UpdateJournal
}

func LinkedReader(cloud CloudReader, base *Journal, state *UpdateJournal) (CloudReader, error) {
	if state == nil {
		return cloud, nil
	}
	if err := state.validate(base); err != nil {
		return nil, err
	}
	return &linkedCloud{CloudReader: cloud, base: base, state: state}, nil
}

func (c *linkedCloud) Get(ctx context.Context, r Resource) (*Resource, error) {
	got, err := c.CloudReader.Get(ctx, r)
	if err != nil || r.Kind != "Role" || len(c.state.Operations) == 0 {
		return got, err
	}
	last := len(c.state.Operations) - 1
	for last >= 0 && c.state.Operations[last].Phase == "Abandoned" {
		last--
	}
	if last < 0 {
		return got, nil
	}
	op := &c.state.Operations[last]
	step, ok := op.Roles[r.Name]
	if !ok {
		return nil, fmt.Errorf("role is missing from linked ownership journal")
	}
	expected := step.Before
	if step.State == "Verified" {
		expected = *step.After
	} else if step.State == "Intent" {
		// Unknown requests are resolved by readback of exactly before or
		// authorized after content; never infer success from a 404.
		for _, want := range op.Intent.Retention.Resources {
			if want.Kind == "Role" && want.Name == r.Name {
				want.Owner = c.base.Owner()
				if sameResource(want, *got) {
					expected = *got
				}
			}
		}
	}
	if !sameResource(expected, *got) || expected.UID != got.UID {
		// During cleanup a soft-deleted role is valid only if the cleanup
		// engine already journaled that exact deletion intent.
		step := (*Step)(nil)
		if c.state.Cleanup != nil {
			step = c.state.Cleanup.Steps[resourceKey(r)]
		}
		if !(got.Deleted && step != nil && step.State == "Deleting" && got.Owner == expected.Owner) {
			return nil, fmt.Errorf("linked role ownership, permissions or etag changed")
		}
	}
	for _, original := range c.base.Plan.Resources {
		if original.Kind == "Role" && original.Name == r.Name {
			copy := original
			copy.Owner = c.base.Owner()
			copy.UID = c.base.Steps[resourceKey(original)].Resource.UID
			copy.Deleted = got.Deleted
			return &copy, nil
		}
	}
	return nil, fmt.Errorf("linked role is not part of original installation")
}

// NewLinkedCleanupEngine reuses all original ownership/teardown checks after
// validating the linked role union. Delete uses a fresh, actual role etag.
func NewLinkedCleanupEngine(cloud Cloud, base *Journal, state *UpdateJournal, store JournalStore) (*Engine, error) {
	reader, err := LinkedReader(cloud, base, state)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return &Engine{Cloud: cloud, Store: store}, nil
	}
	return &Engine{Cloud: &linkedCleaner{linkedCloud: reader.(*linkedCloud), writer: cloud}, Store: store}, nil
}

type linkedCleaner struct {
	*linkedCloud
	writer Cloud
}

func (c *linkedCleaner) Create(context.Context, Resource) (*Resource, error) {
	return nil, fmt.Errorf("linked cleanup cannot create IAM resources")
}
func (c *linkedCleaner) SetPolicy(ctx context.Context, scope string, p *crm.Policy) error {
	return c.writer.SetPolicy(ctx, scope, p)
}
func (c *linkedCleaner) Delete(ctx context.Context, r Resource) error {
	if r.Kind == "Role" {
		if _, err := c.Get(ctx, r); err != nil {
			return err
		}
		actual, err := c.CloudReader.Get(ctx, r)
		if err != nil {
			return err
		}
		// Validate again against the exact lineage, not just the owner label.
		if _, err := c.Get(ctx, *actual); err != nil {
			return err
		}
		r.UID = actual.UID
	}
	return c.writer.Delete(ctx, r)
}

func (c *linkedCleaner) CheckCleanupAuthority(ctx context.Context) error {
	checker, ok := c.writer.(interface{ CheckCleanupAuthority(context.Context) error })
	if !ok {
		return fmt.Errorf("cleanup authority readback is required")
	}
	return checker.CheckCleanupAuthority(ctx)
}
