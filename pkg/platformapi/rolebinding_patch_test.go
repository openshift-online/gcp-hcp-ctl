package platformapi

import (
	"encoding/json"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"testing"
)

func TestRoleBindingPatchConditionsTriState(t *testing.T) {
	empty := make([]gcpv1.RoleBindingRequirement, 0)
	populated := []gcpv1.RoleBindingRequirement{{Key: "region", Operator: "NotIn", Values: []string{"us-central1"}}}
	for _, tc := range []struct {
		patch RoleBindingPatch
		want  string
	}{
		{RoleBindingPatch{}, `{}`},
		{RoleBindingPatch{Conditions: &empty}, `{"conditions":[]}`},
		{RoleBindingPatch{Conditions: &populated}, `{"conditions":[{"key":"region","operator":"NotIn","values":["us-central1"]}]}`},
	} {
		b, err := json.Marshal(tc.patch)
		if err != nil || string(b) != tc.want {
			t.Fatalf("%s %v want %s", b, err, tc.want)
		}
	}
}
