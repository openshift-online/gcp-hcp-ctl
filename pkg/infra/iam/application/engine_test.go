package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
)

type memoryJournal struct {
	data              []byte
	saveCount, failAt int
}

func (s *memoryJournal) Save(j *Journal) error {
	s.saveCount++
	if s.failAt == s.saveCount {
		return errors.New("simulated disk failure")
	}
	s.data, _ = json.Marshal(j)
	return nil
}
func (s *memoryJournal) load(t *testing.T) *Journal {
	t.Helper()
	var j Journal
	if err := json.Unmarshal(s.data, &j); err != nil {
		t.Fatal(err)
	}
	return &j
}

type fakeCloud struct {
	resources       map[string]Resource
	policies        map[string]*crm.Policy
	writes, creates int
	conflicts       int
	failAfterCreate bool
	rejectAt        int
	rejectCode      int
}

func fakeBackend(p *Plan) *fakeCloud {
	f := &fakeCloud{resources: map[string]Resource{}, policies: map[string]*crm.Policy{}}
	worker := Resource{Kind: "ServiceAccount", Name: "projects/" + p.Identity.Project + "/serviceAccounts/" + p.Identity.WorkerEmail, UID: p.Identity.WorkerUID}
	f.resources[resourceKey(worker)] = worker
	f.policies["project"] = &crm.Policy{Version: 3, Etag: "etag", Bindings: []*crm.Binding{{Role: "roles/viewer", Members: []string{"user:foreign@example.test"}, Condition: &crm.Expr{Expression: "true", Title: "unrelated"}}}, AuditConfigs: []*crm.AuditConfig{{Service: "allServices"}}}
	f.policies[worker.Name] = &crm.Policy{Version: 3, Etag: "etag", Bindings: []*crm.Binding{{Role: "roles/iam.serviceAccountUser", Members: []string{"user:unrelated@example.test"}}}}
	return f
}
func (f *fakeCloud) ProjectNumber(context.Context) (string, error) { return "123456789", nil }
func (f *fakeCloud) Get(_ context.Context, r Resource) (*Resource, error) {
	v, ok := f.resources[resourceKey(r)]
	if !ok {
		return nil, &googleapi.Error{Code: 404}
	}
	return &v, nil
}
func (f *fakeCloud) Create(_ context.Context, r Resource) (*Resource, error) {
	f.writes++
	f.creates++
	if f.creates == f.rejectAt {
		return nil, &googleapi.Error{Code: f.rejectCode}
	}
	if _, ok := f.resources[resourceKey(r)]; ok {
		return nil, &googleapi.Error{Code: 409}
	}
	r.UID = "uid-" + r.Name
	f.resources[resourceKey(r)] = r
	if r.Kind == "ServiceAccount" {
		f.policies[r.Name] = &crm.Policy{Etag: "etag", Version: 3}
	}
	if f.failAfterCreate {
		f.failAfterCreate = false
		return nil, errors.New("transport lost after commit")
	}
	return &r, nil
}

func TestDefinitiveCreateFailureCanResumeOrCleanOwnedPartial(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 422, 429, 409, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			e, j, f, _ := testEngine(t)
			f.rejectAt = 2
			f.rejectCode = code
			if err := e.Apply(t.Context(), j); err == nil {
				t.Fatal("rejected create succeeded")
			}
			step := j.Steps[resourceKey(j.Plan.Resources[1])]
			if code == 409 || code == 500 {
				if step.State != "Unknown" {
					t.Fatal("ambiguous failure treated as no effect")
				}
				before := f.writes
				if err := e.Apply(t.Context(), j); err == nil || f.writes != before {
					t.Fatal("ambiguous absent create was blindly retried")
				}
				return
			}
			if step.State != "Failed" {
				t.Fatalf("definitive rejection became %s", step.State)
			}
			if err := e.Apply(t.Context(), j); err != nil {
				t.Fatal(err)
			}
			if len(f.resources) != 15 {
				t.Fatal("resume did not complete exactly one resource set")
			}
		})
	}
	e, j, f, _ := testEngine(t)
	f.rejectAt = 2
	f.rejectCode = 403
	if err := e.Apply(t.Context(), j); err == nil {
		t.Fatal("expected rejection")
	}
	if err := e.Cleanup(t.Context(), j, func(context.Context, *Journal) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(f.resources) != 1 {
		t.Fatal("partial cleanup did not preserve only default worker")
	}
}
func (f *fakeCloud) Delete(_ context.Context, r Resource) error {
	f.writes++
	delete(f.resources, resourceKey(r))
	return nil
}
func (f *fakeCloud) GetPolicy(_ context.Context, scope string) (*crm.Policy, error) {
	p, ok := f.policies[scope]
	if !ok {
		return nil, &googleapi.Error{Code: 404}
	}
	b, _ := json.Marshal(p)
	var out crm.Policy
	_ = json.Unmarshal(b, &out)
	return &out, nil
}
func (f *fakeCloud) SetPolicy(_ context.Context, scope string, p *crm.Policy) error {
	f.writes++
	if p.Version != 3 || p.Etag == "" {
		return errors.New("policy v3 CAS missing")
	}
	if f.conflicts > 0 {
		f.conflicts--
		f.policies[scope].Bindings = append(f.policies[scope].Bindings, &crm.Binding{Role: "roles/browser", Members: []string{"user:concurrent@example.test"}})
		return &googleapi.Error{Code: 409}
	}
	b, _ := json.Marshal(p)
	var out crm.Policy
	_ = json.Unmarshal(b, &out)
	f.policies[scope] = &out
	return nil
}
func (f *fakeCloud) CheckPermissions(context.Context, []string) error { return nil }
func testEngine(t *testing.T) (*Engine, *Journal, *fakeCloud, *memoryJournal) {
	t.Helper()
	p := testPlan(t)
	j, err := newJournal(p)
	if err != nil {
		t.Fatal(err)
	}
	f := fakeBackend(p)
	s := &memoryJournal{}
	if err := s.Save(j); err != nil {
		t.Fatal(err)
	}
	return &Engine{Cloud: f, Store: s, Recheck: func(context.Context) error { return nil }, Pause: func(context.Context) error { return nil }}, j, f, s
}

func TestReadOnlyInspectionAndBlockedTarget(t *testing.T) {
	e, j, f, _ := testEngine(t)
	if err := e.Inspect(t.Context(), &j.Plan, nil); err != nil {
		t.Fatal(err)
	}
	if f.writes != 0 {
		t.Fatal("preview mutated cloud")
	}
	j.Plan.Target.Block = "WIFConfigTemplateUnverified"
	j.Plan.Target.Template.Verified = new(false)
	if err := e.Apply(t.Context(), j); err == nil || f.writes != 0 {
		t.Fatal("unverified contract applied")
	}
}

func TestControlledFirstRoleStopHasNoGrantsOrCoverageAndCanClean(t *testing.T) {
	e, j, f, s := testEngine(t)
	e.StopAfterFirstRole = true
	if err := e.Apply(t.Context(), j); !errors.Is(err, ErrControlledStop) {
		t.Fatalf("expected bounded stop: %v", err)
	}
	j = s.load(t)
	if f.creates != 1 || f.writes != 1 || len(f.resources) != 2 || j.VerifiedAt != nil || j.Phase != "PausedAfterFirstRole" || len(j.Steps) != 1 {
		t.Fatal("bounded rehearsal exceeded one unbound role or claimed coverage")
	}
	if err := e.Apply(t.Context(), j); !errors.Is(err, ErrControlledStop) {
		t.Fatalf("same bounded resume did not pause: %v", err)
	}
	if f.creates != 1 || f.writes != 1 {
		t.Fatal("bounded resume repeated a cloud mutation")
	}
	if err := e.Verify(t.Context(), &j.Plan, j); err == nil {
		t.Fatal("partial rehearsal verified complete IAM")
	}
	if err := e.Cleanup(t.Context(), j, func(context.Context, *Journal) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(f.resources) != 1 || j.Phase != "Cleaned" {
		t.Fatal("bounded rehearsal did not clean only its role")
	}
}

func TestControlledStopCannotRelabelAnAdvancedOperation(t *testing.T) {
	e, j, f, _ := testEngine(t)
	if err := e.Apply(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	before := f.writes
	e.StopAfterFirstRole = true
	if err := e.Apply(t.Context(), j); err == nil || errors.Is(err, ErrControlledStop) || f.writes != before {
		t.Fatal("advanced operation was mislabeled as a one-role rehearsal")
	}
}
func TestApplyVerifyCASAndOwnedCleanup(t *testing.T) {
	e, j, f, _ := testEngine(t)
	f.conflicts = 1
	if err := e.Apply(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	if f.creates != 14 || j.VerifiedAt == nil {
		t.Fatalf("unexpected apply result %d", f.creates)
	}
	if err := e.Verify(t.Context(), &j.Plan, j); err != nil {
		t.Fatal(err)
	}
	worker := "projects/test-project/serviceAccounts/123456789-compute@developer.gserviceaccount.com"
	if !policyHas(f.policies[worker], Grant{Role: "roles/iam.serviceAccountUser", Member: "user:unrelated@example.test"}) {
		t.Fatal("worker policy foreign member lost")
	}
	if err := e.Cleanup(t.Context(), j, func(context.Context, *Journal) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(f.resources) != 1 || f.resources["ServiceAccount/"+worker].UID != "worker-uid" {
		t.Fatal("cleanup touched default worker or left owned IAM")
	}
	project := f.policies["project"]
	if len(project.AuditConfigs) != 1 || len(project.Bindings) != 2 || project.Bindings[0].Condition == nil {
		t.Fatalf("unrelated project policy damaged: %#v", project)
	}
	if len(f.policies[worker].Bindings) != 1 || !reflect.DeepEqual(f.policies[worker].Bindings[0].Members, []string{"user:unrelated@example.test"}) {
		t.Fatal("worker policy was replaced")
	}
	writes := f.writes
	if err := e.Cleanup(t.Context(), j, func(context.Context, *Journal) error { return nil }); err != nil || f.writes != writes {
		t.Fatal("cleanup was not idempotent")
	}
	if err := e.Apply(t.Context(), j); err == nil {
		t.Fatal("retired operation reused")
	}
}
func TestUnknownCreateResolvesWithoutDuplicate(t *testing.T) {
	e, j, f, s := testEngine(t)
	f.failAfterCreate = true
	if err := e.Apply(t.Context(), j); err == nil || j.VerifiedAt != nil {
		t.Fatal("uncertain create reported success")
	}
	j = s.load(t)
	if err := e.Apply(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	if f.creates != 14 {
		t.Fatalf("resume duplicated creates: %d", f.creates)
	}
}
func TestJournalFailureBeforeMutationAndAfterCreate(t *testing.T) {
	for _, failAt := range []int{2, 3} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			e, j, f, s := testEngine(t)
			s.failAt = failAt
			if err := e.Apply(t.Context(), j); err == nil {
				t.Fatal("disk failure ignored")
			}
			if failAt == 2 && f.writes != 0 {
				t.Fatal("write-ahead failure still mutated cloud")
			}
			if failAt == 3 {
				j = s.load(t)
				s.failAt = 0
				if err := e.Apply(t.Context(), j); err != nil {
					t.Fatal(err)
				}
				if f.creates != 14 {
					t.Fatal("lost journal response duplicated creation")
				}
			}
		})
	}
}
func TestForeignCollisionAndRecreatedSARefused(t *testing.T) {
	e, j, f, _ := testEngine(t)
	r := j.Plan.Resources[0]
	r.Owner = "foreign"
	f.resources[resourceKey(r)] = r
	if err := e.Apply(t.Context(), j); err == nil || f.writes != 0 {
		t.Fatal("foreign collision adopted")
	}
	delete(f.resources, resourceKey(r))
	if err := e.Apply(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	for key, r := range f.resources {
		if r.Kind == "ServiceAccount" && r.Owner == j.Owner() {
			r.UID = "replacement"
			f.resources[key] = r
			break
		}
	}
	if err := e.Verify(t.Context(), &j.Plan, j); err == nil {
		t.Fatal("replacement account verified")
	}
}
func TestCleanupRequiresProofAndRejectsUnknownSubmission(t *testing.T) {
	e, j, f, _ := testEngine(t)
	if err := e.Apply(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	before := f.writes
	j.Cluster.State = "Unknown"
	if err := e.Cleanup(t.Context(), j, func(context.Context, *Journal) error { return nil }); err == nil || f.writes != before {
		t.Fatal("unknown submission cleaned")
	}
	j.Cluster.State = "None"
	if err := e.Cleanup(t.Context(), j, func(context.Context, *Journal) error { return errors.New("active dependencies") }); err == nil || f.writes != before {
		t.Fatal("failed teardown proof ignored")
	}
}
func TestFileJournalProtectionLockAndExplicitResume(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "operation.json")
	p := testPlan(t)
	f, err := Open(path, p, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, p, true); err == nil {
		t.Fatal("concurrent operation lock ignored")
	}
	_ = f.Close()
	if _, err := Open(path, p, false); err == nil {
		t.Fatal("existing state did not require resume")
	}
	f, err = Open(path, p, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("public state file accepted")
	}
}

func TestCleanupUnknownResourceDoesNotRemoveAnyGrant(t *testing.T) {
	e, j, f, _ := testEngine(t)
	if err := e.Apply(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	before := f.writes
	j.Steps[resourceKey(j.Plan.Resources[0])].State = "Unknown"
	delete(f.resources, resourceKey(j.Plan.Resources[0]))
	if err := e.Cleanup(t.Context(), j, func(context.Context, *Journal) error { return nil }); err == nil || f.writes != before {
		t.Fatal("cleanup changed policies before resolving all creation outcomes")
	}
}
