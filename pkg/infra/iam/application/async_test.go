package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	gcpiam "github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/iam"
	"google.golang.org/api/googleapi"
)

type asyncFake struct {
	*fakeCloud
	store         *memoryJournal
	operations    map[string]Resource
	failPool      bool
	readErrorCode int
}

func (f *asyncFake) StartCreate(_ context.Context, r Resource) (string, error) {
	f.writes++
	f.creates++
	name := "operation-" + r.Kind
	f.operations[name] = r
	return name, nil
}
func (f *asyncFake) WaitCreate(_ context.Context, name string) error {
	r := f.operations[name]
	var snapshot Journal
	if err := json.Unmarshal(f.store.data, &snapshot); err != nil {
		return err
	}
	step := snapshot.Steps[resourceKey(r)]
	if step == nil || step.Resource == nil || step.Resource.Operation != name {
		return &gcpiam.OperationFailure{Code: 3}
	}
	if f.readErrorCode != 0 {
		code := f.readErrorCode
		f.readErrorCode = 0
		return &googleapi.Error{Code: code}
	}
	if f.failPool && r.Kind == "Pool" {
		f.failPool = false
		return &gcpiam.OperationFailure{Code: 7}
	}
	r.UID = "created-" + r.Name
	f.resources[resourceKey(r)] = r
	return nil
}

func TestOperationPollDenialDoesNotMeanCreateHadNoEffect(t *testing.T) {
	e, j, base, store := testEngine(t)
	f := &asyncFake{fakeCloud: base, store: store, operations: map[string]Resource{}, readErrorCode: 403}
	e.Cloud = f
	if err := e.Apply(t.Context(), j); err == nil {
		t.Fatal("operation poll denial ignored")
	}
	var pool Resource
	for _, r := range j.Plan.Resources {
		if r.Kind == "Pool" {
			pool = r
		}
	}
	step := j.Steps[resourceKey(pool)]
	if step.State != "Unknown" || step.Resource == nil || step.Resource.Operation == "" {
		t.Fatal("read denial discarded an in-flight create")
	}
	before := f.writes
	if err := e.Cleanup(t.Context(), j, func(context.Context, *Journal) error { return nil }); err == nil || f.writes != before {
		t.Fatal("poll denial allowed cleanup of an unknown create")
	}
	if err := e.Apply(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	if f.creates != 14 {
		t.Fatal("resume started a second pool")
	}
}

func TestAsyncOperationPersistedBeforeWaitAndTerminalFailureResumes(t *testing.T) {
	e, j, base, store := testEngine(t)
	f := &asyncFake{fakeCloud: base, store: store, operations: map[string]Resource{}, failPool: true}
	e.Cloud = f
	if err := e.Apply(t.Context(), j); err == nil {
		t.Fatal("terminal pool failure reported success")
	}
	var pool Resource
	for _, r := range j.Plan.Resources {
		if r.Kind == "Pool" {
			pool = r
		}
	}
	if j.Steps[resourceKey(pool)].State != "Failed" {
		t.Fatal("terminal absent operation remained unknown")
	}
	if j.Steps[resourceKey(pool)].LastFailedOperation == "" {
		t.Fatal("failed operation identity was discarded")
	}
	if err := e.Apply(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	if len(f.resources) != 15 || f.creates != 15 {
		t.Fatal("terminal no-effect resume duplicated a resource")
	}
	if j.Steps[resourceKey(pool)].Resource.Operation == "" {
		t.Fatal("operation identity was lost after readback")
	}
}

func TestOrphanPlaceholderRequiresExplicitProtectedResume(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, testPlan(t), false); err == nil {
		t.Fatal("orphan file was silently overwritten by a fresh operation")
	}
	f, err := Open(path, testPlan(t), true)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Journal.Phase != "Provisioning" || len(f.Journal.Steps) != 0 {
		t.Fatal("orphan recovery invented operation outcomes")
	}
}
