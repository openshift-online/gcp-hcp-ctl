package rolebinding

import (
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"

	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
)

func TestConditionPreview(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", "<none>"}, {" ", " "}, {strings.Repeat("é", 39), strings.Repeat("é", 39)},
		{strings.Repeat("é", 40), strings.Repeat("é", 40)}, {strings.Repeat("é", 41), strings.Repeat("é", 40) + "..."},
		{"a\tb\nc\r\x1b", `a\tb\nc\r\u001b`},
		{strings.Repeat("x", 39) + "\t", strings.Repeat("x", 39) + "..."},
		{strings.Repeat("x", 35) + "\x1b", strings.Repeat("x", 35) + "..."},
	} {
		got := conditionPreview(&tc.input)
		if got != tc.want {
			t.Errorf("preview %q: got %q want %q", tc.input, got, tc.want)
		}
		if !utf8.ValidString(got) {
			t.Error("invalid UTF-8")
		}
	}
	if got := conditionPreview(nil); got != "<none>" {
		t.Fatal(got)
	}
}

func TestConditionShellRoundTrip(t *testing.T) {
	ranShells := 0
	inputs := []string{"", "normal", " 'single' \"double\" $(no) `x` ", "line 1\nline 2\n", "\t\x1b[31m\x7f's\\x", "unicode café\u202e hidden"}
	for _, shell := range []string{"bash", "zsh"} {
		path, err := exec.LookPath(shell)
		if err != nil {
			t.Logf("%s unavailable", shell)
			continue
		}
		ranShells++
		t.Logf("running shell round trips with %s", path)
		for _, input := range inputs {
			// Only controlled test fixtures are ever evaluated as shell code.
			literal := conditionArgument(&input)
			cmd := exec.Command(path, "-c", "printf '%s' "+literal)
			got, err := cmd.Output()
			if err != nil || string(got) != input {
				t.Errorf("%s %q: %q, %v; literal %q", shell, input, got, err, literal)
			}
		}
	}
	if ranShells == 0 {
		t.Fatal("shell round-trip coverage requires at least one supported shell (bash or zsh) on PATH")
	}
}

func TestPublicConditionDeepCopy(t *testing.T) {
	for _, c := range []*string{nil, str(""), str("condition")} {
		b := &gcpv1.RoleBinding{Spec: gcpv1.RoleBindingSpec{Condition: c}}
		copy := b.DeepCopy()
		if c == nil {
			if copy.Spec.Condition != nil {
				t.Fatal("nil condition changed")
			}
			continue
		}
		if copy.Spec.Condition == c || *copy.Spec.Condition != *c {
			t.Fatal("condition not copied")
		}
		*copy.Spec.Condition = "changed"
		if *c == "changed" {
			t.Fatal("aliased condition")
		}
	}
}
