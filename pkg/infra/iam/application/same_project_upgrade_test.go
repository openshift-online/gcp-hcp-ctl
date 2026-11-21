package application

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
)

// Keep the existing HTTP fixture but replace its initial/target identities with
// the selected pair and byte-equivalent executable IAM content.
func zeroDelta422Fixture(t *testing.T) *updateFixture {
	t.Helper()
	f := newUpdateFixture(t)
	source := testTarget(t)
	source.Version = "4.22.21"
	source.Reference.Name = "4.22.0"
	p, err := Build(source, f.base.Plan.Identity)
	if err != nil {
		t.Fatal(err)
	}
	j, err := newJournal(p)
	if err != nil {
		t.Fatal(err)
	}
	f.cloud = fakeBackend(p)
	engine := &Engine{Cloud: f.cloud, Store: &memoryJournal{}, Recheck: func(context.Context) error { return nil }}
	if err := engine.Apply(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	j.Phase = "ClusterSubmitted"
	j.Cluster = Submission{State: "Confirmed", Name: "owned", Namespace: "test-project", UID: "cluster-uid"}
	f.base = j
	f.cluster.Spec.Release.Version = source.Version
	f.cluster.Spec.Platform.GCP.IAM.TemplateApplications = []v1.WIFConfigTemplateReference{source.Reference}
	f.cluster.Status.IAMRequirements.Dispatched.ReleaseVersion = source.Version
	f.cluster.Status.IAMRequirements.Dispatched.TemplateRef = source.Reference
	f.pools.Items[0].Status.IAMRequirements.Dispatched = f.cluster.Status.IAMRequirements.Dispatched.DeepCopy()
	f.old.Name = source.Version
	f.old.Spec.WIFConfigTemplateRef = &source.Reference
	f.history.Name = source.Reference.Name
	f.history.Spec = *source.Template.DeepCopy()
	f.version.Name = "5.0.0"
	targetRef := v1.WIFConfigTemplateReference{Name: "5.0.0", UID: "new-template-uid", Digest: source.Reference.Digest}
	f.version.Spec.WIFConfigTemplateRef = &targetRef
	f.target.Name = targetRef.Name
	f.target.Spec = *source.Template.DeepCopy()
	j.Cluster.Payload, err = json.Marshal(f.cluster)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func Test42221ZeroPermissionDeltaStillRecordsDistinctTarget(t *testing.T) {
	f := zeroDelta422Fixture(t)
	baseDigest := digestJSON(f.base)
	before := f.cloud.writes
	p, err := PreviewUpdate(t.Context(), f.client, f.cloud, f.base, "owned", "5.0.0", "candidate", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !p.NewApplication || len(p.RoleAdditions) != 0 || p.Retention.Target.Reference.Name != "5.0.0" || p.Retention.Target.Reference.Digest != f.base.Plan.Target.Reference.Digest {
		t.Fatal("equal permissions collapsed distinct raw references")
	}
	j := &UpdateJournal{Kind: "IAMUpdates", Schema: 1, PrimaryDigest: baseDigest}
	if err := PrepareUpdate(t.Context(), f.cloud, f.base, j, p, p.PlanFingerprint); err != nil {
		t.Fatal(err)
	}
	w := &roleWriter{cloud: f.cloud}
	e := &UpdateEngine{Client: f.client, Cloud: f.cloud, Writer: w, Store: &memoryUpdateStore{}}
	for i := 0; i < 2; i++ {
		if err := e.Apply(t.Context(), f.base, j); err != nil {
			t.Fatal(err)
		}
	}
	apps := f.cluster.Spec.Platform.GCP.IAM.TemplateApplications
	if w.calls != 0 || f.cloud.writes != before || f.writes != 1 || len(apps) != 2 || apps[0] != f.base.Plan.Target.Reference || apps[1] != *f.version.Spec.WIFConfigTemplateRef || f.cluster.Spec.Release.Version != "4.22.21" {
		t.Fatal("zero-delta update changed permissions/release, lost history or repeated assertion")
	}
	if digestJSON(f.base) != baseDigest || j.Operations[0].Phase != "Recorded" {
		t.Fatal("primary changed or update incomplete")
	}
	if err := VerifyLinked(t.Context(), f.cloud, f.base, j); err != nil {
		t.Fatal(err)
	}
}

func Test42221ZeroDeltaIneligibleTargetCannotPrepare(t *testing.T) {
	f := zeroDelta422Fixture(t)
	f.target.Spec.Verified = new(false)
	p, err := PreviewUpdate(t.Context(), f.client, f.cloud, f.base, "owned", "5.0.0", "candidate", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	before := f.cloud.writes
	j := &UpdateJournal{Kind: "IAMUpdates", Schema: 1, PrimaryDigest: digestJSON(f.base)}
	if err := PrepareUpdate(t.Context(), f.cloud, f.base, j, p, p.PlanFingerprint); err == nil || f.cloud.writes != before || f.writes != 0 {
		t.Fatal("equal permission set bypassed eligibility")
	}
}

type isolatedPolicyCloud struct {
	*fakeCloud
	staleWrites int
}

func (c *isolatedPolicyCloud) SetPolicy(ctx context.Context, scope string, p *crm.Policy) error {
	if p.Etag != c.policies[scope].Etag {
		c.staleWrites++
		return &googleapi.Error{Code: 409}
	}
	if err := c.fakeCloud.SetPolicy(ctx, scope, p); err != nil {
		return err
	}
	c.policies[scope].Etag = fmt.Sprintf("fresh-policy-etag-%d", c.writes)
	return nil
}

func TestSameProjectTwoInstallationsKeepOwnershipAndSharedWorker(t *testing.T) {
	first := testPlan(t)
	id := first.Identity
	id.InfraID = "second-123"
	id.Issuer = "https://issuer.example/" + id.InfraID
	second, err := Build(&first.Target, id)
	if err != nil {
		t.Fatal(err)
	}
	c := &isolatedPolicyCloud{fakeCloud: fakeBackend(first)}
	j1, err := newJournal(first)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := newJournal(second)
	if err != nil {
		t.Fatal(err)
	}
	e1 := &Engine{Cloud: c, Store: &memoryJournal{}, Recheck: func(context.Context) error { return nil }}
	e2 := &Engine{Cloud: c, Store: &memoryJournal{}, Recheck: func(context.Context) error { return nil }}
	if err := e1.Apply(t.Context(), j1); err != nil {
		t.Fatal(err)
	}
	beforeJournal := digestJSON(j1)
	beforeResources := map[string]Resource{}
	for k, r := range c.resources {
		beforeResources[k] = r
	}
	worker := "projects/" + first.Identity.Project + "/serviceAccounts/" + first.Identity.WorkerEmail
	etag := c.policies[worker].Etag
	if err := e2.Inspect(t.Context(), second, j1); err == nil {
		t.Fatal("foreign installation journal accepted")
	}
	if err := e2.Inspect(t.Context(), first, nil); err == nil {
		t.Fatal("existing installation adopted")
	}
	if err := e2.Apply(t.Context(), j2); err != nil {
		t.Fatal(err)
	}
	if c.policies[worker].Etag == etag {
		t.Fatal("shared policy fixture did not exercise changed etags")
	}
	if err := e1.Verify(t.Context(), first, j1); err != nil {
		t.Fatalf("second installation broke first verification: %v", err)
	}
	for _, g := range append(append([]Grant{}, first.Grants...), second.Grants...) {
		if !policyHas(c.policies[g.Scope], g) {
			t.Fatal("lost an installation grant")
		}
	}
	if err := e2.Cleanup(t.Context(), j2, func(context.Context, *Journal) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if c.staleWrites != 0 || digestJSON(j1) != beforeJournal {
		t.Fatal("stale policy write or mutation of first journal")
	}
	if !reflect.DeepEqual(beforeResources, c.resources) {
		t.Fatal("second cleanup removed/modified original resources or shared worker")
	}
	if err := e1.Verify(t.Context(), first, j1); err != nil {
		t.Fatal(err)
	}
	for _, g := range second.Grants {
		if policyHas(c.policies[g.Scope], g) {
			t.Fatal("second grant left after cleanup")
		}
	}
	if len(c.policies["project"].AuditConfigs) != 1 || c.policies["project"].Bindings[0].Condition == nil || !policyHas(c.policies[worker], Grant{Role: "roles/iam.serviceAccountUser", Member: "user:unrelated@example.test"}) {
		t.Fatal("unrelated policy content lost")
	}
}

func TestWrongOwnerCleanupStopsBeforeAnyWrite(t *testing.T) {
	e, j, c, _ := testEngine(t)
	if err := e.Apply(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	r := j.Plan.Resources[0]
	foreign := c.resources[resourceKey(r)]
	foreign.Owner = "different-installation-owner"
	c.resources[resourceKey(r)] = foreign
	before := c.writes
	if err := e.Cleanup(t.Context(), j, func(context.Context, *Journal) error { return nil }); err == nil || c.writes != before {
		t.Fatal("wrong-owner cleanup wrote to cloud")
	}
}
