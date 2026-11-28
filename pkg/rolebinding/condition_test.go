package rolebinding

import (
	"encoding/json"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestParseCondition(t *testing.T) {
	for _, input := range []string{`name In ["demo"]`, `region NotIn ["é*"]`, " \tregion In [\"a\", \"b\", \"a\"]\r\n", `name NotIn ["AND OR", " x ", "\u002a", "\\*"]`, "name In [\n\"a\"\n]"} {
		r, err := parseCondition(input)
		if err != nil {
			t.Errorf("%q: %v", input, err)
			continue
		}
		again, err := parseCondition(mustFormatCondition(t, r))
		if err != nil || !reflect.DeepEqual(r, again) {
			t.Fatalf("roundtrip %v %v", r, err)
		}
	}
	for _, input := range []string{`name == "x"`, `name != "x"`, `name=="x"`, `region != "x"`, "", " \t", `true`, `Name == "x"`, `name in ["x"]`, `name In["x"]`, `nameIn ["x"]`, `name = = "x"`, `name == null`, `name In null`, `name In []`, `name In [null]`, `name In ["x",null]`, `name In [1]`, `name In [true]`, `name In [[]]`, `name In [{}]`, `name In [""]`, `name In ["\u2003"]`, `name In [" "]`, `name In ["x",]`, `name In ["\*"]`, `name In ["x"] AND name In ["y"]`, `name In ["x"] {}`, "name\u00a0In [\"x\"]"} {
		if r, err := parseCondition(input); err == nil {
			t.Errorf("accepted %q: %#v", input, r)
		}
	}
}
func FuzzParseCondition(f *testing.F) {
	for _, s := range []string{`name == "x"`, `region In ["a", "b*"]`, `name In [null]`, "name In [\n\"x\"]", `name != "\\*"`, `name == "x" trailing`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := parseCondition(s)
		if err != nil {
			return
		}
		next, err := parseCondition(mustFormatCondition(t, r))
		if err != nil || !reflect.DeepEqual(r, next) {
			t.Fatalf("unstable %#v %v", r, err)
		}
	})
}
func TestConditionPreview(t *testing.T) {
	if mustConditionPreview(t, nil) != "<none>" {
		t.Fatal("missing placeholder")
	}
	for _, value := range []string{"x", "\n\t\x1b", strings.Repeat("é", 80)} {
		r := gcpv1.RoleBindingRequirement{Key: "name", Operator: "In", Values: []string{value}}
		got := mustConditionPreview(t, []gcpv1.RoleBindingRequirement{r})
		if utf8.RuneCountInString(got) > 43 || strings.ContainsAny(got, "\n\t\x1b") {
			t.Fatalf("unsafe preview %q", got)
		}
	}
	r := gcpv1.RoleBindingRequirement{Key: "name", Operator: "In"}
	if mustFormatCondition(t, r) != "name In []" {
		t.Fatal(mustFormatCondition(t, r))
	}
}
func TestPublicConditionDeepCopy(t *testing.T) {
	b := &gcpv1.RoleBinding{Spec: gcpv1.RoleBindingSpec{Conditions: []gcpv1.RoleBindingRequirement{{Key: "name", Operator: "In", Values: []string{"x"}}}}}
	c := b.DeepCopy()
	c.Spec.Conditions[0].Values[0] = "y"
	c.Spec.Conditions[0].Key = "region"
	if b.Spec.Conditions[0].Values[0] != "x" || b.Spec.Conditions[0].Key != "name" {
		t.Fatal("aliased")
	}
	if _, err := json.Marshal(c); err != nil {
		t.Fatal(err)
	}
}

func TestDetailConditions(t *testing.T) {
	for _, tc := range []struct {
		conditions []any
		want       string
	}{
		{nil, "Conditions:\n  None\n"},
		{wireConditions("demo*"), "Conditions:\n  - name In [\"demo*\"]\n"},
		{append(wireConditions("x"), wireConditions("y")...), "Conditions:\n  - name In [\"x\"]\n  - name In [\"y\"]\n"},
		{[]any{map[string]any{"key": "region", "operator": "NotIn", "values": []any{}}}, "Conditions:\n  - region NotIn []\n"},
	} {
		out, _, err := executeBinding(t, func(w http.ResponseWriter, r *http.Request) {
			response(w, obj(map[string]any{"subject": "a@b", "roleRef": map[string]any{"kind": "Role", "name": "r"}, "conditions": tc.conditions}))
		}, "get", "grant")
		if err != nil || !strings.HasSuffix(out, tc.want) {
			t.Fatalf("%v %q want suffix %q", err, out, tc.want)
		}
	}
}

func TestCanonicalControlEscapes(t *testing.T) {
	for _, v := range []string{"\x00\x1b\u009b\u200b", "'\"\\\né"} {
		r := gcpv1.RoleBindingRequirement{Key: "name", Operator: "In", Values: []string{v}}
		out := mustFormatCondition(t, r)
		for _, c := range out {
			if !unicode.IsPrint(c) {
				t.Fatalf("unsafe %q", out)
			}
		}
		back, err := parseCondition(out)
		if err != nil || !reflect.DeepEqual(back, r) {
			t.Fatalf("%q %v", out, err)
		}
	}
	// A JSON escape straddling the boundary must be dropped as a whole token.
	r := gcpv1.RoleBindingRequirement{Key: "name", Operator: "In", Values: []string{strings.Repeat("a", 27) + "\x1b"}}
	p := mustConditionPreview(t, []gcpv1.RoleBindingRequirement{r})
	if strings.HasSuffix(strings.TrimSuffix(p, "..."), `\u0`) {
		t.Fatal(p)
	}
}

func mustFormatCondition(t *testing.T, r gcpv1.RoleBindingRequirement) string {
	t.Helper()
	result, err := formatCondition(r)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func mustConditionPreview(t *testing.T, requirements []gcpv1.RoleBindingRequirement) string {
	t.Helper()
	result, err := conditionPreview(requirements)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
