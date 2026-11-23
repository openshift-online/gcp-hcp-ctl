package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"google.golang.org/api/googleapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type memoryUpdateStore struct {
	data          []byte
	fail          bool
	count, failAt int
}

func (m *memoryUpdateStore) SaveUpdate(j *UpdateJournal) error {
	m.count++
	if m.fail || m.failAt == m.count {
		return errors.New("simulated update journal write failure")
	}
	m.data, _ = json.Marshal(j)
	return nil
}
func (m *memoryUpdateStore) reload(t *testing.T) *UpdateJournal {
	t.Helper()
	var j UpdateJournal
	if err := json.Unmarshal(m.data, &j); err != nil {
		t.Fatal(err)
	}
	return &j
}

type roleWriter struct {
	cloud                   *fakeCloud
	calls                   int
	lost, conflict, missing bool
	deny                    bool
	afterWrite              func()
}

func (w *roleWriter) CheckRoleUpdateAuthority(context.Context) error {
	if w.deny {
		return errors.New("role update authority denied")
	}
	return nil
}

func (w *roleWriter) UpdateRolePermissions(_ context.Context, r Resource) error {
	w.calls++
	key := resourceKey(r)
	got, ok := w.cloud.resources[key]
	if !ok || w.missing {
		return &googleapi.Error{Code: 404}
	}
	if got.UID != r.UID || w.conflict {
		w.conflict = false
		return &googleapi.Error{Code: 409}
	}
	got.Permissions = slices.Clone(r.Permissions)
	got.UID = fmt.Sprintf("updated-etag-%d", w.calls)
	w.cloud.resources[key] = got
	if w.afterWrite != nil {
		w.afterWrite()
	}
	if w.lost {
		w.lost = false
		return errors.New("connection lost after role update")
	}
	return nil
}

func prepareTestUpdate(t *testing.T) (*updateFixture, *UpdateJournal, *UpdateEngine, *memoryUpdateStore, *roleWriter) {
	t.Helper()
	f := newUpdateFixture(t)
	p, err := PreviewUpdate(t.Context(), f.client, f.cloud, f.base, "owned", f.version.Name, "candidate", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	j := &UpdateJournal{Kind: "IAMUpdates", Schema: 1, PrimaryDigest: digestJSON(f.base)}
	if err := PrepareUpdate(t.Context(), f.cloud, f.base, j, p, p.PlanFingerprint); err != nil {
		t.Fatal(err)
	}
	s := &memoryUpdateStore{}
	w := &roleWriter{cloud: f.cloud}
	e := &UpdateEngine{Client: f.client, Cloud: f.cloud, Writer: w, Store: s}
	return f, j, e, s, w
}

func TestUpdateEngineAddsPermissionsThenRecordsAndPreservesOriginal(t *testing.T) {
	f, j, e, _, w := prepareTestUpdate(t)
	before := digestJSON(f.base)
	if err := e.Apply(t.Context(), f.base, j); err != nil {
		t.Fatal(err)
	}
	if w.calls != 1 || f.writes != 1 || j.Operations[0].Phase != "Recorded" {
		t.Fatalf("bad update outcome: writes=%d records=%d phase=%s", w.calls, f.writes, j.Operations[0].Phase)
	}
	if len(f.cluster.Spec.Platform.GCP.IAM.TemplateApplications) != 2 || f.cluster.Spec.Release.Version != f.old.Name {
		t.Fatal("assertion changed release or lost original history")
	}
	if err := VerifyLinked(t.Context(), f.cloud, f.base, j); err != nil {
		t.Fatal(err)
	}
	if err := (&Engine{Cloud: f.cloud}).Verify(t.Context(), &f.base.Plan, f.base); err == nil {
		t.Fatal("old verifier unexpectedly accepted changed role etag/content")
	}
	if before != digestJSON(f.base) {
		t.Fatal("update modified original ownership journal")
	}
	if err := e.Apply(t.Context(), f.base, j); err != nil || w.calls != 1 || f.writes != 1 {
		t.Fatalf("recorded resume repeated writes: %v", err)
	}
}

func TestUpdateEngineUnknownRoleAndAssertionRecoverWithoutDuplicateWrites(t *testing.T) {
	for _, kind := range []string{"role", "assertion"} {
		t.Run(kind, func(t *testing.T) {
			f, j, e, s, w := prepareTestUpdate(t)
			if kind == "role" {
				w.lost = true
			} else {
				f.patchLost = true
			}
			if err := e.Apply(t.Context(), f.base, j); err == nil {
				t.Fatal("lost response reported success")
			}
			j = s.reload(t)
			// Recreate the engine as after a process restart.
			e = &UpdateEngine{Client: f.client, Cloud: f.cloud, Writer: w, Store: s}
			if err := e.Apply(t.Context(), f.base, j); err != nil {
				t.Fatal(err)
			}
			if w.calls != 1 || f.writes != 1 || len(f.cluster.Spec.Platform.GCP.IAM.TemplateApplications) != 2 || j.Operations[0].Phase != "Recorded" {
				t.Fatal("recovery repeated a cloud write or assertion")
			}
		})
	}
}

func TestUpdateEngineFaultsKeepAssertionBlocked(t *testing.T) {
	for _, kind := range []string{"journal", "role404", "revoked", "inventory", "foreign-role", "conflict"} {
		t.Run(kind, func(t *testing.T) {
			f, j, e, s, w := prepareTestUpdate(t)
			switch kind {
			case "journal":
				s.fail = true
			case "role404":
				w.missing = true
			case "revoked":
				f.target.Spec.Verified = new(false)
			case "inventory":
				f.pools.Items[0].Spec.NodeLabels = map[string]string{"new": "value"}
			case "foreign-role":
				for key, r := range f.cloud.resources {
					if r.Kind == "Role" {
						r.Owner = "foreign"
						f.cloud.resources[key] = r
						break
					}
				}
			case "conflict":
				w.conflict = true
			}
			if err := e.Apply(t.Context(), f.base, j); err == nil || f.writes != 0 || len(f.cluster.Spec.Platform.GCP.IAM.TemplateApplications) != 1 {
				t.Fatalf("fault released assertion: %v", err)
			}
		})
	}
}

func TestUpdateEngineRecordFailureAndStatusOnlyRefresh(t *testing.T) {
	f, j, e, s, w := prepareTestUpdate(t)
	f.patchFailure = 403
	if err := e.Apply(t.Context(), f.base, j); err == nil || j.Operations[0].Phase != "AssertionIntent" || w.calls != 1 {
		t.Fatalf("record failure lost cloud-success state: %v", err)
	}
	j = s.reload(t)
	f.patchFailure = 0
	w.deny = true // no role writes are left; recording verified coverage is allowed.
	f.cluster.ResourceVersion = "new-status-rv"
	f.pools.ResourceVersion = "new-list-rv"
	f.pools.Items[0].ResourceVersion = "new-pool-rv"
	if err := e.Apply(t.Context(), f.base, j); err != nil {
		t.Fatal(err)
	}
	if w.calls != 1 || j.Operations[0].Phase != "Recorded" {
		t.Fatal("status-only refresh changed immutable operation identity")
	}
}

func TestUpdateFingerprintStableAcrossFreshStatusSnapshots(t *testing.T) {
	f := newUpdateFixture(t)
	p, err := PreviewUpdate(t.Context(), f.client, f.cloud, f.base, "owned", f.version.Name, "candidate", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	f.cluster.ResourceVersion = "status-only"
	f.pools.ResourceVersion = "namespace-list-only"
	f.pools.Items[0].ResourceVersion = "pool-status-only"
	now := metav1.NewTime(time.Now().Add(-time.Second))
	f.cluster.Status.IAMRequirements.LastRefreshTime = &now
	f.pools.Items[0].Status.IAMRequirements.LastRefreshTime = &now
	second, err := PreviewUpdate(t.Context(), f.client, f.cloud, f.base, "owned", f.version.Name, "candidate", time.Now().UTC())
	if err != nil || second.PlanFingerprint != p.PlanFingerprint {
		t.Fatalf("status refresh invalidated unchanged reviewed intent: %v", err)
	}
	j := &UpdateJournal{Kind: "IAMUpdates", Schema: 1, PrimaryDigest: digestJSON(f.base)}
	if err := PrepareUpdate(t.Context(), f.cloud, f.base, j, second, p.PlanFingerprint); err != nil {
		t.Fatal(err)
	}
}

func TestRecordedUpdateSupportsNextSameContractOperation(t *testing.T) {
	f, j, e, _, w := prepareTestUpdate(t)
	if err := e.Apply(t.Context(), f.base, j); err != nil {
		t.Fatal(err)
	}
	p, err := PreviewLinkedUpdate(t.Context(), f.client, f.cloud, f.base, j, "owned", f.version.Name, "candidate", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if p.NewApplication || len(p.RoleAdditions) != 0 || len(p.Retention.Required) != 2 {
		t.Fatal("next preview forgot recorded contract or repeated additions")
	}
	if err := PrepareUpdate(t.Context(), f.cloud, f.base, j, p, p.PlanFingerprint); err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(t.Context(), f.base, j); err != nil {
		t.Fatal(err)
	}
	if len(j.Operations) != 2 || w.calls != 1 || f.writes != 1 {
		t.Fatal("same-contract next operation wrote roles or duplicate assertion")
	}
}

func TestUnstartedAbandonLeavesTombstoneAndProtectsOriginalOpen(t *testing.T) {
	f, j, e, s, _ := prepareTestUpdate(t)
	if err := s.SaveUpdate(j); err != nil {
		t.Fatal(err)
	}
	if err := AbandonUnstartedUpdate(f.base, j); err != nil {
		t.Fatal(err)
	}
	if j.Operations[0].Phase != "Abandoned" {
		t.Fatal("abandon removed rather than retained intent")
	}
	if err := e.Apply(t.Context(), f.base, j); err == nil {
		t.Fatal("abandoned operation resumed")
	}
	if _, err := PreviewLinkedUpdate(t.Context(), f.client, f.cloud, f.base, j, "owned", f.version.Name, "candidate", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	p, err := PreviewLinkedUpdate(t.Context(), f.client, f.cloud, f.base, j, "owned", f.version.Name, "candidate", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareUpdate(t.Context(), f.cloud, f.base, j, p, p.PlanFingerprint); err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(t.Context(), f.base, j); err != nil {
		t.Fatal(err)
	}
	if j.Operations[0].Phase != "Abandoned" || j.Operations[1].Phase != "Recorded" {
		t.Fatal("new operation erased abandoned intent")
	}
	f, j, e, s, _ = prepareTestUpdate(t)
	f.patchFailure = 403
	if err := e.Apply(t.Context(), f.base, j); err == nil {
		t.Fatal("assertion fault did not occur")
	}
	j = s.reload(t)
	if err := AbandonUnstartedUpdate(f.base, j); err == nil {
		t.Fatal("attempted IAM changes were abandoned")
	}
}

func TestIntentWithThirdStateNeverRetriesOrRecreates(t *testing.T) {
	f, j, e, s, w := prepareTestUpdate(t)
	w.lost = true
	if err := e.Apply(t.Context(), f.base, j); err == nil {
		t.Fatal("unknown role result not exercised")
	}
	j = s.reload(t)
	for key, r := range f.cloud.resources {
		if r.Kind == "Role" && strings.HasPrefix(r.UID, "updated-") {
			r.Permissions = append(r.Permissions, "compute.instances.unreviewed")
			f.cloud.resources[key] = r
		}
	}
	if err := e.Apply(t.Context(), f.base, j); err == nil || w.calls != 1 || f.writes != 0 {
		t.Fatalf("third-state role drift was adopted or retried: %v", err)
	}
}

func TestRoleReadbackJournalFailureRecoversSavedIntent(t *testing.T) {
	f, j, e, s, w := prepareTestUpdate(t)
	w.afterWrite = func() { s.fail = true }
	if err := e.Apply(t.Context(), f.base, j); err == nil {
		t.Fatal("lost durable role readback was reported complete")
	}
	s.fail = false
	w.afterWrite = nil
	j = s.reload(t)
	if err := e.Apply(t.Context(), f.base, j); err != nil {
		t.Fatal(err)
	}
	if w.calls != 1 || f.writes != 1 || j.Operations[0].Phase != "Recorded" {
		t.Fatal("saved intent recovery repeated cloud mutation")
	}
}

func TestMultipleDistinctContractsRetainFirstUpdateAndCleanup(t *testing.T) {
	f, j, e, _, w := prepareTestUpdate(t)
	if err := e.Apply(t.Context(), f.base, j); err != nil {
		t.Fatal(err)
	}
	previous := f.target.DeepCopy()
	f.archivedTemplates = map[string]*v1.WIFConfigTemplate{"/apis/gcp.managed.openshift.io/v1/wifconfigtemplates/" + previous.Name: previous}
	f.target = f.target.DeepCopy()
	f.target.Name = "5.0.2"
	f.target.UID = "third-template"
	f.target.Spec.IAM.Roles[0].Permissions = append(f.target.Spec.IAM.Roles[0].Permissions, "compute.instances.stop")
	digest, err := Digest(f.target.Spec)
	if err != nil {
		t.Fatal(err)
	}
	f.version = f.version.DeepCopy()
	f.version.Name = "5.0.2"
	f.version.UID = "third-version"
	f.version.Spec.WIFConfigTemplateRef = &v1.WIFConfigTemplateReference{Name: f.target.Name, UID: string(f.target.UID), Digest: digest}
	p, err := PreviewLinkedUpdate(t.Context(), f.client, f.cloud, f.base, j, "owned", f.version.Name, "candidate", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.RoleAdditions) != 1 || len(p.RoleAdditions[0].Permissions) != 1 || p.RoleAdditions[0].Permissions[0] != "compute.instances.stop" {
		t.Fatalf("next diff lost previous contract or repeated permissions: %+v", p.RoleAdditions)
	}
	if err := PrepareUpdate(t.Context(), f.cloud, f.base, j, p, p.PlanFingerprint); err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(t.Context(), f.base, j); err != nil {
		t.Fatal(err)
	}
	if w.calls != 2 || f.writes != 2 || len(f.cluster.Spec.Platform.GCP.IAM.TemplateApplications) != 3 {
		t.Fatal("multiple-contract chain did not converge")
	}
	if err := VerifyLinked(t.Context(), f.cloud, f.base, j); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.cloud.resources {
		if r.Kind == "Role" && slices.Contains(r.Permissions, "compute.instances.stop") && !slices.Contains(r.Permissions, "compute.instances.start") {
			t.Fatal("new update pruned first update's permission")
		}
	}
	b, _ := json.Marshal(f.base)
	var shadow Journal
	_ = json.Unmarshal(b, &shadow)
	j.Cleanup = &shadow
	cleanup, err := NewLinkedCleanupEngine(f.cloud, f.base, j, &memoryJournal{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup.Cleanup(t.Context(), j.Cleanup, func(context.Context, *Journal) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(f.cloud.resources) != 1 {
		t.Fatal("multi-contract cleanup left owned resources")
	}
}

func TestUpdateJournalProtectedPersistenceAndSharedLock(t *testing.T) {
	f, j, _, _, _ := prepareTestUpdate(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(dir, "primary.json")
	b, _ := json.Marshal(f.base)
	if err := os.WriteFile(primary, b, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := OpenUpdates(primary)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	file.State = j
	if err := file.SaveUpdate(j); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadUpdates(primary, f.base)
	if err != nil || !reflect.DeepEqual(loaded, j) {
		t.Fatalf("linked journal did not persist: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(primary, &f.base.Plan, true); err == nil || !strings.Contains(err.Error(), "linked IAM update") {
		t.Fatalf("old mutation path opened primary with sidecar: %v", err)
	}
	file, err = OpenUpdates(primary)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := Open(primary, &f.base.Plan, true); err == nil {
		t.Fatal("concurrent original cleanup acquired installation lock")
	}
	after, _ := os.ReadFile(primary)
	if string(after) != string(b) {
		t.Fatal("sidecar save rewrote original journal")
	}
	info, err := os.Stat(UpdateStatePath(primary))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("sidecar permissions are not private")
	}
	if err := os.Chmod(UpdateStatePath(primary), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUpdates(primary, f.base); err == nil {
		t.Fatal("public linked journal was accepted")
	}
}

func TestLinkedSaveRejectsChangedOriginalAndEmptySidecar(t *testing.T) {
	f, j, _, _, _ := prepareTestUpdate(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(dir, "primary.json")
	data, _ := json.Marshal(f.base)
	if err := os.WriteFile(primary, data, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := OpenUpdates(primary)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.SaveUpdate(j); err != nil {
		t.Fatal(err)
	}
	var modified Journal
	_ = json.Unmarshal(data, &modified)
	modified.NetworkFingerprint = "unexpected"
	changed, _ := json.Marshal(&modified)
	if err := os.WriteFile(primary, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := file.SaveUpdate(j); err == nil {
		t.Fatal("changed original proof was silently accepted")
	}
	if err := os.WriteFile(primary, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(UpdateStatePath(primary), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUpdates(primary, f.base); err == nil {
		t.Fatal("empty authoritative sidecar was treated as no prior operation")
	}
}

func (f *fakeCloud) CheckCleanupAuthority(context.Context) error { return nil }

func TestLinkedCleanupRemovesOwnedUnionOnlyAndBlocksActiveCluster(t *testing.T) {
	f, j, e, _, _ := prepareTestUpdate(t)
	if err := e.Apply(t.Context(), f.base, j); err != nil {
		t.Fatal(err)
	}
	cleanup := f.base
	store := &memoryJournal{}
	engine, err := NewLinkedCleanupEngine(f.cloud, f.base, j, store)
	if err != nil {
		t.Fatal(err)
	}
	denied := func(context.Context, *Journal) error { return errors.New("Cluster still exists") }
	before := f.cloud.writes
	if err := engine.Cleanup(t.Context(), cleanup, denied); err == nil || f.cloud.writes != before {
		t.Fatal("cleanup ignored hosted dependency guard")
	}
	// Clone the original, as UpdateFile does. Keep progress in the sidecar.
	b, _ := json.Marshal(f.base)
	var shadow Journal
	_ = json.Unmarshal(b, &shadow)
	j.Cleanup = &shadow
	if err := engine.Cleanup(t.Context(), j.Cleanup, func(context.Context, *Journal) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if j.Cleanup.Phase != "Cleaned" || len(f.cloud.resources) != 1 {
		t.Fatal("owned union cleanup failed or touched default worker")
	}
	for _, r := range f.cloud.resources {
		if !strings.Contains(r.Name, "-compute@developer.gserviceaccount.com") {
			t.Fatal("default worker was removed")
		}
	}
	if f.base.Phase != "ClusterSubmitted" {
		t.Fatal("cleanup rewrote original installation state")
	}
}
