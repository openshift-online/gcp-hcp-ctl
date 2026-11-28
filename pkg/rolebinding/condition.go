package rolebinding

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/internal/access"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/spf13/cobra"
)

// conditionPreview escapes non-printing code points before counting the display
// length, so escape sequences are never cut in half or interpreted by a terminal.
func conditionPreview(requirements []gcpv1.RoleBindingRequirement) (string, error) {
	if len(requirements) == 0 {
		return "<none>", nil
	}
	parts := make([]string, len(requirements))
	for i, r := range requirements {
		formatted, err := formatCondition(r)
		if err != nil {
			return "", err
		}
		parts[i] = formatted
	}
	condition := strings.Join(parts, " AND ")
	var tokens []string
	total := 0
	for len(condition) > 0 {
		r, n := utf8.DecodeRuneInString(condition)
		if r == '\\' && len(condition) > 1 {
			n = 2
			if condition[1] == 'u' && len(condition) >= 6 {
				n = 6
			}
			token := condition[:n]
			tokens = append(tokens, token)
			total += utf8.RuneCountInString(token)
			condition = condition[n:]
			continue
		}
		condition = condition[n:]
		var token string
		switch r {
		case '\n':
			token = `\n`
		case '\r':
			token = `\r`
		case '\t':
			token = `\t`
		default:
			if unicode.IsPrint(r) {
				token = string(r)
			} else if r <= 0xffff {
				token = fmt.Sprintf(`\u%04x`, r)
			} else {
				token = fmt.Sprintf(`\U%08x`, r)
			}
		}
		tokens = append(tokens, token)
		total += utf8.RuneCountInString(token)
	}
	var b strings.Builder
	count := 0
	for _, token := range tokens {
		n := utf8.RuneCountInString(token)
		if count+n > 40 {
			break
		}
		b.WriteString(token)
		count += n
	}
	if total > 40 {
		b.WriteString("...")
	}
	return b.String(), nil
}

// setConditionHelp appends shared guidance after Cobra's normal usage,
// examples, and flags, rather than putting it in the command description.
func setConditionHelp(cmd *cobra.Command) {
	cmd.SetHelpTemplate(cmd.HelpTemplate() + "\n" + conditionHelp + "\n")
}

const conditionHelp = `Conditions:
  Repeat --condition for requirements ANDed together. Keys: region, name.
  Use In/NotIn with whitespace on both sides and a nonempty JSON string
  array. Only In and NotIn are supported.
  Example: --condition 'region In ["us-east1", "europe-*"]'
  Shell single quotes protect the JSON double quotes. Only ASCII JSON
  whitespace is accepted between tokens. Blank values are rejected.
  '*' is the sole wildcard, including after JSON decoding; backslashes do
  not escape it. Other characters are literal. No literal-star matching.
  Omission creates an unconditional grant or preserves conditions on update.
  Supplied update conditions replace the complete list, never append.
  Empty-string conditions are invalid; update --clear-conditions clears.
  Region requirements do not match non-regional resources; no resource-type
  exemption or global deny is introduced.

Safety:
  Independent grants remain additive. Changes may take time to propagate.
  Keep a separate administrator grant to avoid access-management lockout.
  Clearing conditions broadens this grant.`

var conditionPattern = regexp.MustCompile(`(?s)^(region|name)[ \t\r\n]+(In|NotIn)[ \t\r\n]+(.*)$`)

func parseCondition(input string) (gcpv1.RoleBindingRequirement, error) {
	var result gcpv1.RoleBindingRequirement
	// Dot must include operand newlines, while token whitespace stays ASCII-only.
	input = strings.Trim(input, " \t\r\n")
	m := conditionPattern.FindStringSubmatch(input)
	if m == nil {
		return result, fmt.Errorf("expected region/name In/NotIn JSON array")
	}
	var value any
	if err := json.Unmarshal([]byte(m[3]), &value); err != nil {
		return result, fmt.Errorf("invalid JSON operand: %w", err)
	}
	op := m[2]
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return result, fmt.Errorf("operand must be a nonempty JSON string array")
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		v, ok := item.(string)
		if !ok || strings.TrimSpace(v) == "" {
			return result, fmt.Errorf("values must be nonblank JSON strings")
		}
		values = append(values, v)
	}
	result.Key = gcpv1.RoleBindingConditionKey(m[1])
	result.Operator = gcpv1.RoleBindingConditionOperator(op)
	result.Values = values
	return result, nil
}

func conditionArgs(inputs *[]string, parsed *[]gcpv1.RoleBindingRequirement, update bool) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := access.Named(cmd, args); err != nil {
			return err
		}
		if update && cmd.Flags().Changed("condition") && cmd.Flags().Changed("clear-conditions") {
			return fmt.Errorf("--condition and --clear-conditions are mutually exclusive")
		}
		*parsed = nil
		for i, input := range *inputs {
			r, err := parseCondition(input)
			if err != nil {
				return fmt.Errorf("condition %d: %w", i+1, err)
			}
			*parsed = append(*parsed, r)
		}
		return nil
	}
}

func formatCondition(r gcpv1.RoleBindingRequirement) (string, error) {
	values := make([]string, len(r.Values))
	for i, v := range r.Values {
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("encoding condition value: %w", err)
		}
		safe := make([]string, 0, len(b))
		for _, c := range string(b) {
			if unicode.IsPrint(c) {
				safe = append(safe, string(c))
			} else if c <= 0xffff {
				safe = append(safe, fmt.Sprintf(`\u%04x`, c))
			} else {
				hi, lo := utf16.EncodeRune(c)
				safe = append(safe, fmt.Sprintf(`\u%04x\u%04x`, hi, lo))
			}
		}
		values[i] = strings.Join(safe, "")
	}
	return string(r.Key) + " " + string(r.Operator) + " [" + strings.Join(values, ", ") + "]", nil
}
