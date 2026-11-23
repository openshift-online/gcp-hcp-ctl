package application

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func testUpdatePlan(t *testing.T, base *Plan, mutate func(*Plan)) *Plan {
	t.Helper()
	other := clonePlan(base)
	other.Target.Version = "5.0.1"
	other.Target.Reference.Name = "5.0.1"
	other.Target.Reference.UID = "new-template-uid"
	other.Target.Template.IAM.Roles[0].Permissions = append(other.Target.Template.IAM.Roles[0].Permissions, "compute.instances.start")
	var err error
	other.Target.Reference.Digest, err = Digest(other.Target.Template)
	if err != nil {
		t.Fatal(err)
	}
	other, err = Build(&other.Target, other.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(other)
	}
	return other
}

func TestRetainPermissionsPreservesAllRunningRolesWithoutMutation(t *testing.T) {
	base := testPlan(t)
	target := testUpdatePlan(t, base, nil)
	oldBase, oldTarget := clonePlan(base), clonePlan(target)
	result, err := RetainPermissions(target, base)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(base, oldBase) || !reflect.DeepEqual(target, oldTarget) {
		t.Fatal("retention mutated its input plans")
	}
	if result.Target.Reference != target.Target.Reference || !slices.Equal(result.Grants, base.Grants) {
		t.Fatal("target reference or exact grants changed")
	}
	if result.Identity != target.Identity || len(result.Required) != 1 || result.Required[0].Reference != base.Target.Reference {
		t.Fatal("identity or retained contract pin was not recorded")
	}
	result.Required[0].Template.IAM.Roles[0].Permissions[0] = "compute.instances.replaced"
	if !reflect.DeepEqual(base, oldBase) {
		t.Fatal("preview aliases the running template")
	}
	for i := range result.Resources {
		want := slices.Clone(target.Resources[i].Permissions)
		want = append(want, base.Resources[i].Permissions...)
		slices.Sort(want)
		want = slices.Compact(want)
		if !slices.Equal(result.Resources[i].Permissions, want) {
			t.Errorf("resource %s did not retain running permissions", result.Resources[i].Name)
		}
	}
}

func TestRetainPermissionsSameContractAndMultipleRequirements(t *testing.T) {
	base := testPlan(t)
	other := testUpdatePlan(t, base, nil)
	result, err := RetainPermissions(base, other, base)
	if err != nil {
		t.Fatal(err)
	}
	if result.Target.Reference != base.Target.Reference {
		t.Fatal("target switched to a retained version")
	}
	if len(result.Required) != 2 || result.Required[0].Reference != other.Target.Reference || result.Required[1].Reference != base.Target.Reference {
		t.Fatal("running and pending requirements lost their recorded order")
	}
	for i := range result.Resources {
		if result.Resources[i].Kind == "Role" && strings.Contains(strings.Join(other.Resources[i].Permissions, ","), "compute.instances.start") && !slices.Contains(result.Resources[i].Permissions, "compute.instances.start") {
			t.Fatal("older pending permission was lost")
		}
	}
	result, err = RetainPermissions(base, base)
	if err != nil || !reflect.DeepEqual(result.Resources, base.Resources) || !reflect.DeepEqual(result.Grants, base.Grants) {
		t.Fatalf("same-contract update changed scope: %v", err)
	}
}

func TestRetainPermissionsKeepsPermissionDroppedByTarget(t *testing.T) {
	running := testPlan(t)
	target := testUpdatePlan(t, running, nil)
	var removed, roleName string
	for i := range target.Target.Template.IAM.Roles {
		role := &target.Target.Template.IAM.Roles[i]
		if len(role.Permissions) > 1 {
			removed = role.Permissions[0]
			roleName = role.Name
			role.Permissions = slices.Clone(role.Permissions[1:])
			break
		}
	}
	if removed == "" {
		t.Fatal("fixture has no removable permission")
	}
	var err error
	target.Target.Reference.Digest, err = Digest(target.Target.Template)
	if err != nil {
		t.Fatal(err)
	}
	target, err = Build(&target.Target, target.Identity)
	if err != nil {
		t.Fatal(err)
	}
	result, err := RetainPermissions(target, running)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range result.Resources {
		if !slices.IsSorted(r.Permissions) {
			t.Fatal("permissions are not sorted")
		}
		if strings.HasSuffix(r.Name, "_"+roleName) && slices.Contains(r.Permissions, removed) {
			found = true
		}
	}
	for _, r := range target.Resources {
		if strings.HasSuffix(r.Name, "_"+roleName) && slices.Contains(r.Permissions, removed) {
			t.Fatal("target did not actually drop the running role permission")
		}
	}
	if !found {
		t.Fatal("permission still required by running contract was lost")
	}
}

func TestRetainPermissionsRejectsTopologyAndSyntheticInputs(t *testing.T) {
	base := testPlan(t)
	for name, mutate := range map[string]func(*Plan){
		"project":  func(p *Plan) { p.Identity.Project = "other-project" },
		"grant":    func(p *Plan) { p.Grants[0].Member = "serviceAccount:foreign@example.com" },
		"wif":      func(p *Plan) { p.Resources[len(p.Resources)-1].Issuer = "https://other.example" },
		"role":     func(p *Plan) { p.Resources[0].Name += "Other" },
		"addition": func(p *Plan) { p.Resources = append(p.Resources, Resource{Kind: "Role", Name: "unexpected"}) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := testUpdatePlan(t, base, mutate)
			if _, err := RetainPermissions(bad, base); err == nil {
				t.Fatal("synthetic or foreign target was accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*Plan){
		"different worker": func(p *Plan) { p.Identity.WorkerUID = "different" },
		"different binding": func(p *Plan) {
			p.Target.Template.IAM.RoleBindings[0].ServiceAccountRef = "cloud-network"
		},
		"different subject": func(p *Plan) {
			p.Target.Template.IAM.ServiceAccounts[0].WorkloadIdentitySubjects[0] = "system:serviceaccount:other:identity"
		},
	} {
		t.Run(name, func(t *testing.T) {
			target := testUpdatePlan(t, base, nil)
			mutate(target)
			if name != "different worker" {
				var err error
				target.Target.Reference.Digest, err = Digest(target.Target.Template)
				if err != nil {
					t.Fatal(err)
				}
				target, err = Build(&target.Target, target.Identity)
				if err != nil {
					return // Rejection at Build is also fail-closed.
				}
			}
			if _, err := RetainPermissions(target, base); err == nil {
				t.Fatal("changed identity or binding topology was accepted")
			}
		})
	}
	if _, err := RetainPermissions(base); err == nil {
		t.Fatal("unknown running contract set was accepted")
	}
	if _, err := RetainPermissions(base, nil); err == nil {
		t.Fatal("missing retained contract was accepted")
	}
	if _, err := RetainPermissions(base, base, nil); err == nil {
		t.Fatal("nil contract in running set was accepted")
	}
}

func TestRetainPermissionsRejectsOversizedUnion(t *testing.T) {
	base := testPlan(t)
	role := &base.Target.Template.IAM.Roles[0]
	for i := 0; len(role.Permissions) < 512; i++ {
		role.Permissions = append(role.Permissions, fmt.Sprintf("compute.instances.test%03d", i))
	}
	var err error
	base.Target.Reference.Digest, err = Digest(base.Target.Template)
	if err != nil {
		t.Fatal(err)
	}
	base, err = Build(&base.Target, base.Identity)
	if err != nil {
		t.Fatal(err)
	}
	target := clonePlan(base)
	target.Target.Reference.Name = "5.0.1"
	target.Target.Reference.UID = "new-uid"
	target.Target.Version = "5.0.1"
	target.Target.Template.IAM.Roles[0].Permissions[0] = "compute.instances.extra"
	target.Target.Reference.Digest, err = Digest(target.Target.Template)
	if err != nil {
		t.Fatal(err)
	}
	target, err = Build(&target.Target, target.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RetainPermissions(target, base); err == nil || !strings.Contains(err.Error(), "permission limit") {
		t.Fatalf("oversized union accepted: %v", err)
	}
}
