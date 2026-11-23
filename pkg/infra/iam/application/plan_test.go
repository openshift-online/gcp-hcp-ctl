package application

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
)

const goldenDigest = "sha256:c0b4c3dbdd48d1925bf7c172ac0385639d00092f083b5cac11865e83c3acea71"

func testTarget(t *testing.T) *Target {
	t.Helper()
	b, err := os.ReadFile("testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec v1.WIFConfigTemplateSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	spec.Verified = new(true)
	return &Target{Version: "5.0.0-rc.8", VersionUID: "version-uid", VersionGeneration: 2, ChannelGroup: "candidate", Reference: v1.WIFConfigTemplateReference{Name: "5.0.0", UID: "template-uid", Digest: goldenDigest}, Template: spec}
}
func testPlan(t *testing.T) *Plan {
	t.Helper()
	p, err := Build(testTarget(t), Identity{APIEndpoint: "https://api.example", Project: "test-project", ProjectNumber: "123456789", InfraID: "fresh-123", Issuer: "https://issuer.example/fresh-123", WorkerEmail: "123456789-compute@developer.gserviceaccount.com", WorkerUID: "worker-uid"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGoldenDigestAndPlan(t *testing.T) {
	target := testTarget(t)
	before := *target.Template.DeepCopy()
	got, err := Digest(target.Template)
	if err != nil || got != goldenDigest {
		t.Fatalf("digest=%s err=%v", got, err)
	}
	if !reflect.DeepEqual(before, target.Template) {
		t.Fatal("digest mutated source")
	}
	p := testPlan(t)
	if len(p.Resources) != 14 || len(p.Grants) != 15 {
		t.Fatalf("unexpected resource/grant counts: %d %d", len(p.Resources), len(p.Grants))
	}
	for _, g := range p.Grants {
		if g.Role == "roles/iam.serviceAccountUser" && g.Scope != "projects/test-project/serviceAccounts/123456789-compute@developer.gserviceaccount.com" {
			t.Fatal("actAs broadened")
		}
		if g.Scope == "project" && !strings.HasPrefix(g.Role, "projects/test-project/roles/hcp_") {
			t.Fatal("predefined project role fallback")
		}
	}
	copy, err := Build(&p.Target, p.Identity)
	if err != nil || copy.Fingerprint() != p.Fingerprint() {
		t.Fatal("plan is not deterministic")
	}
	copy.Target.Block = "WIFConfigTemplateUnverified"
	copy.Target.Template.Verified = new(false)
	if copy.Fingerprint() != p.Fingerprint() {
		t.Fatal("eligibility changed immutable plan identity")
	}
}

func TestInvalidIdentityRejectedBeforePlanning(t *testing.T) {
	for _, change := range []func(*Identity){func(i *Identity) { i.InfraID = "trailing-" }, func(i *Identity) { i.InfraID = "too-long-infra-id" }, func(i *Identity) { i.WorkerEmail = "foreign@example.com" }, func(i *Identity) { i.WorkerUID = "" }, func(i *Identity) { i.Issuer = "http://issuer.example/fresh-123" }, func(i *Identity) { i.Issuer = "https://issuer.example/different" }, func(i *Identity) { i.ProjectNumber = "0" }} {
		i := testPlan(t).Identity
		change(&i)
		if _, err := Build(testTarget(t), i); err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
}

func TestPlanFingerprintIgnoresContractSetOrdering(t *testing.T) {
	p := testPlan(t)
	target := testTarget(t)
	target.Template.IAM.ServiceAccounts[0], target.Template.IAM.ServiceAccounts[1] = target.Template.IAM.ServiceAccounts[1], target.Template.IAM.ServiceAccounts[0]
	target.Template.IAM.Roles[0], target.Template.IAM.Roles[1] = target.Template.IAM.Roles[1], target.Template.IAM.Roles[0]
	other, err := Build(target, p.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if other.Fingerprint() != p.Fingerprint() {
		t.Fatal("semantically identical contract changed the review fingerprint")
	}
}

func TestPublicContractFieldInventoryRequiresProtocolReview(t *testing.T) {
	for typ, want := range map[reflect.Type][]string{
		reflect.TypeOf(v1.WIFConfigTemplateSpec{}): {"Verified", "SchemaVersion", "IAM"},
		reflect.TypeOf(v1.WIFConfigIAM{}):          {"RequiredComponents", "Naming", "ServiceAccounts", "Roles", "RoleBindings", "WorkloadIdentityBindings", "ServiceAccountRoleBindings"},
		reflect.TypeOf(v1.WIFConfigNaming{}):       {"MaxInfraIDLength", "MaxCustomRoleIDLength", "MaxServiceAccountIDLength", "MaxWorkloadIdentityIDLength", "CustomRoleIDTemplate", "WorkloadIdentityPoolIDTemplate", "WorkloadIdentityProviderIDTemplate"},
	} {
		var got []string
		for n := 0; n < typ.NumField(); n++ {
			got = append(got, typ.Field(n).Name)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("public contract changed: %s; explicitly review digest/executor semantics", typ.Name())
		}
	}
}
