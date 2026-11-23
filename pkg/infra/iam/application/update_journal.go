package application

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
)

// UpdateJournal is a sidecar, never a replacement for the original ownership
// journal. Each operation is immutable intent plus per-role outcome tracking.
type UpdateJournal struct {
	Kind          string            `json:"kind"`
	Schema        int               `json:"schema"`
	PrimaryDigest string            `json:"primaryDigest"`
	Operations    []UpdateOperation `json:"operations"`
	Cleanup       *Journal          `json:"cleanup,omitempty"`
}

type UpdateOperation struct {
	ID       string                 `json:"id"`
	Intent   UpdatePreview          `json:"intent"`
	Identity string                 `json:"identity"`
	Phase    string                 `json:"phase"`
	Roles    map[string]RoleOutcome `json:"roles"`
}

type RoleOutcome struct {
	Before Resource  `json:"before"`
	After  *Resource `json:"after,omitempty"`
	State  string    `json:"state"` // Planned, Intent, Verified
}

type UpdateStore interface{ SaveUpdate(*UpdateJournal) error }

// UpdateStatePath is fixed per original installation, so a different file name
// cannot evade an unfinished operation or hide retained role requirements.
func UpdateStatePath(primary string) string { return primary + ".updates.json" }

func LoadUpdates(primary string, base *Journal) (*UpdateJournal, error) {
	path := UpdateStatePath(primary)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil, nil
	}
	if err := privateFile(path); err != nil {
		return nil, fmt.Errorf("cannot read protected linked update journal")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read linked update journal")
	}
	var j UpdateJournal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("incomplete or invalid linked update journal; do not allocate a replacement")
	}
	if err := j.validate(base); err != nil {
		return nil, err
	}
	return &j, nil
}

// OpenUpdates holds the original installation's lock shared with create and
// destroy, but never writes its original bytes. New update intent must be saved
// to the linked file and fsynced before the first role RPC.
func OpenUpdates(primary string) (*UpdateFile, error) {
	base, err := Load(primary)
	if err != nil {
		return nil, err
	}
	file, err := openJournal(primary, &base.Plan, true, true)
	if err != nil {
		return nil, err
	}
	state, err := LoadUpdates(primary, file.Journal)
	if err != nil {
		file.Close()
		return nil, err
	}
	if state == nil {
		state = &UpdateJournal{Kind: "IAMUpdates", Schema: 1, PrimaryDigest: digestJSON(file.Journal)}
	}
	return &UpdateFile{Primary: file, State: state, path: UpdateStatePath(primary)}, nil
}

type UpdateFile struct {
	Primary *FileJournal
	State   *UpdateJournal
	path    string
}

func (f *UpdateFile) Close() error { return f.Primary.Close() }

func (f *UpdateFile) SaveUpdate(j *UpdateJournal) error {
	if f.Primary.lock == nil {
		return fmt.Errorf("installation lock must be held")
	}
	base, err := Load(f.Primary.Path)
	if err != nil || digestJSON(base) != j.PrimaryDigest {
		return fmt.Errorf("original ownership journal changed during update")
	}
	if err := j.validate(base); err != nil {
		return err
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode linked update journal")
	}
	if _, err := os.Lstat(f.path); os.IsNotExist(err) {
		// Fully write/fsync a private temp inode before publishing it with an
		// exclusive hard link. A crash can leave only an ignored temp, never
		// an empty authoritative sidecar that would brick recovery.
		tmp, err := os.CreateTemp(filepath.Dir(f.path), ".gcphcpctl-update-")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		_, writeErr := tmp.Write(append(data, '\n'))
		if writeErr == nil {
			writeErr = tmp.Sync()
		}
		closeErr := tmp.Close()
		if writeErr != nil || closeErr != nil {
			return fmt.Errorf("cannot persist initial update intent")
		}
		if err := os.Link(tmp.Name(), f.path); err != nil {
			return fmt.Errorf("linked journal already exists or cannot be published")
		}
		dir, err := os.Open(filepath.Dir(f.path))
		if err != nil {
			return err
		}
		defer dir.Close()
		return dir.Sync()
	}
	return atomicPrivateSave(f.path, data)
}

// Save persists teardown progress to the sidecar only, leaving original proof
// untouched. Network and IAM cleanup share the same canonical original plan.
func (f *UpdateFile) Save(j *Journal) error {
	f.State.Cleanup = j
	return f.SaveUpdate(f.State)
}

func (f *UpdateFile) CurrentJournal() *Journal {
	if f.State.Cleanup != nil {
		return f.State.Cleanup
	}
	b, _ := json.Marshal(f.Primary.Journal)
	var copy Journal
	_ = json.Unmarshal(b, &copy)
	f.State.Cleanup = &copy
	return f.State.Cleanup
}

func (f *FileJournal) CurrentJournal() *Journal { return f.Journal }

func operationIdentity(p UpdatePreview) string {
	// Snapshots expire during ordinary status refreshes. Bind operation intent
	// to exact specs/contracts/payload, not those short-lived resource versions.
	p.ClusterResourceVersion, p.NodePoolListVersion, p.PlanFingerprint = "", "", ""
	p.TargetEligible, p.TargetBlock, p.NewApplication = false, "", false
	p.RoleAdditions = nil
	p.NodePools = slices.Clone(p.NodePools)
	for i := range p.NodePools {
		p.NodePools[i].ResourceVersion = ""
	}
	if p.Retention != nil {
		copy := *p.Retention
		copy.Target.Template = canonicalSpec(copy.Target.Template)
		copy.Target.Block = ""
		copy.Required = slices.Clone(copy.Required)
		for i := range copy.Required {
			copy.Required[i].Template = canonicalSpec(copy.Required[i].Template)
			copy.Required[i].Block = ""
		}
		p.Retention = &copy
	}
	return digestJSON(p)
}

func (j *UpdateJournal) validate(base *Journal) error {
	if base == nil || base.validate(nil) != nil || j.Kind != "IAMUpdates" || j.Schema != 1 || j.PrimaryDigest != digestJSON(base) {
		return fmt.Errorf("linked update journal does not match original ownership proof")
	}
	seen := map[string]bool{}
	for i := range j.Operations {
		op := &j.Operations[i]
		if len(op.ID) != 32 || seen[op.ID] || op.Identity != operationIdentity(op.Intent) || op.Intent.ClusterUID != base.Cluster.UID || op.Intent.PayloadCatalogBinding == "" || op.Intent.Retention == nil {
			return fmt.Errorf("invalid linked update intent")
		}
		if _, err := hex.DecodeString(op.ID); err != nil {
			return fmt.Errorf("invalid linked update operation ID")
		}
		seen[op.ID] = true
		if i+1 < len(j.Operations) && op.Phase != "Recorded" && op.Phase != "Abandoned" {
			return fmt.Errorf("unfinished update precedes another operation")
		}
		switch op.Phase {
		case "Prepared", "Applying", "CloudVerified", "AssertionIntent", "Recorded", "Abandoned":
		default:
			return fmt.Errorf("invalid update phase")
		}
		r := op.Intent.Retention
		if op.Intent.PayloadCatalogBinding != digestJSON([]any{r.Target.VersionUID, r.Target.VersionGeneration}) {
			return fmt.Errorf("target payload catalog binding changed")
		}
		target, err := Build(&r.Target, r.Identity)
		if err != nil || r.Identity != base.Plan.Identity {
			return fmt.Errorf("invalid update target contract")
		}
		required := make([]*Plan, 0, len(r.Required))
		for _, t := range r.Required {
			p, err := Build(&t, r.Identity)
			if err != nil {
				return err
			}
			required = append(required, p)
		}
		union, err := RetainPermissions(target, required...)
		if err != nil || !reflect.DeepEqual(union.Resources, r.Resources) || !reflect.DeepEqual(union.Grants, r.Grants) {
			return fmt.Errorf("retained update union does not match pinned contracts")
		}
		if !slices.ContainsFunc(r.Required, func(t Target) bool { return t.Reference == base.Plan.Target.Reference }) {
			return fmt.Errorf("update omitted original contract")
		}
		for _, resource := range r.Resources {
			if resource.Kind != "Role" {
				continue
			}
			step, ok := op.Roles[resource.Name]
			if !ok || step.Before.Kind != "Role" || step.Before.Name != resource.Name || step.Before.UID == "" || step.Before.Owner != base.Owner() {
				return fmt.Errorf("update role ownership/etag missing")
			}
			prior, err := priorRole(base, j, i, resource.Name)
			if err != nil || !sameResource(prior, step.Before) || prior.UID != step.Before.UID {
				return fmt.Errorf("update role baseline differs from ownership lineage")
			}
			if step.State != "Planned" && step.State != "Intent" && step.State != "Verified" {
				return fmt.Errorf("invalid update role state")
			}
			if op.Phase == "Abandoned" && step.State != "Planned" {
				return fmt.Errorf("attempted role writes cannot be abandoned")
			}
			for _, keep := range step.Before.Permissions {
				if !slices.Contains(resource.Permissions, keep) {
					return fmt.Errorf("update would prune permissions")
				}
			}
			if step.State == "Verified" {
				want := resource
				want.Owner = base.Owner()
				if step.After == nil || step.After.UID == "" || !sameResource(want, *step.After) {
					return fmt.Errorf("verified role does not match retained contract union")
				}
			}
			if op.Phase != "Prepared" && op.Phase != "Applying" && op.Phase != "Abandoned" && step.State != "Verified" {
				return fmt.Errorf("incomplete role update cannot report cloud success")
			}
		}
	}
	if j.Cleanup != nil && (j.Cleanup.validate(nil) != nil || j.Cleanup.Fingerprint != base.Fingerprint || j.Cleanup.Owner() != base.Owner() || j.Cleanup.Cluster.UID != base.Cluster.UID) {
		return fmt.Errorf("invalid linked teardown journal")
	}
	return nil
}

func priorRole(base *Journal, j *UpdateJournal, before int, name string) (Resource, error) {
	for before > 0 && j.Operations[before-1].Phase == "Abandoned" {
		before--
	}
	if before > 0 {
		step, ok := j.Operations[before-1].Roles[name]
		if !ok || step.After == nil {
			return Resource{}, fmt.Errorf("previous update role readback missing")
		}
		return *step.After, nil
	}
	for _, r := range base.Plan.Resources {
		if r.Kind == "Role" && r.Name == name {
			step := base.Steps[resourceKey(r)]
			if step == nil || step.Resource == nil || step.State != "Verified" {
				return Resource{}, fmt.Errorf("original verified role missing")
			}
			return *step.Resource, nil
		}
	}
	return Resource{}, fmt.Errorf("role is not owned by original installation")
}

func newUpdateOperation(p *UpdatePreview) UpdateOperation {
	id := make([]byte, 16)
	// Failure cannot silently fall back to a reusable/predictable operation ID.
	if _, err := rand.Read(id); err != nil {
		return UpdateOperation{}
	}
	return UpdateOperation{ID: hex.EncodeToString(id), Intent: *p, Identity: operationIdentity(*p), Phase: "Prepared", Roles: map[string]RoleOutcome{}}
}

// AbandonUnstartedUpdate appends a terminal tombstone; it never removes intent
// or rolls IAM back. Any intent/verified step requires exact outcome recovery.
func AbandonUnstartedUpdate(base *Journal, state *UpdateJournal) error {
	if state == nil || state.Cleanup != nil || len(state.Operations) == 0 {
		return fmt.Errorf("no unstarted operation to abandon")
	}
	if err := state.validate(base); err != nil {
		return err
	}
	op := &state.Operations[len(state.Operations)-1]
	if op.Phase != "Prepared" {
		return fmt.Errorf("only a prepared, never-attempted update can be abandoned")
	}
	for _, r := range op.Roles {
		if r.State != "Planned" {
			return fmt.Errorf("attempted role write cannot be abandoned")
		}
	}
	op.Phase = "Abandoned"
	return nil
}
