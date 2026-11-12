package rolebinding

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// conditionPreview escapes non-printing code points before counting the display
// length, so escape sequences are never cut in half or interpreted by a terminal.
func conditionPreview(condition *string) string {
	if condition == nil || *condition == "" {
		return "<none>"
	}
	var tokens []string
	total := 0
	for _, r := range *condition {
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
	return b.String()
}

// conditionArgument returns a shell literal. For terminal controls, Bash and
// Zsh ANSI-C quoting encodes their UTF-8 bytes as fixed-width octal escapes.
func conditionArgument(condition *string) string {
	value := ""
	if condition != nil {
		value = *condition
	}
	unsafe := false
	for _, r := range value {
		if r != '\n' && !unicode.IsPrint(r) {
			unsafe = true
			break
		}
	}
	if !unsafe {
		return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
	}
	var b strings.Builder
	b.WriteString("$'")
	for _, r := range value {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		default:
			if unicode.IsPrint(r) {
				b.WriteRune(r)
			} else {
				for _, c := range []byte(string(r)) {
					fmt.Fprintf(&b, `\%03o`, c)
				}
			}
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// setConditionHelp appends shared guidance after Cobra's normal usage,
// examples, and flags, rather than putting it in the command description.
func setConditionHelp(cmd *cobra.Command) {
	cmd.SetHelpTemplate(cmd.HelpTemplate() + "\n" + conditionHelp + "\n")
}

const conditionHelp = `Conditions:
  --condition accepts a Cedar boolean expression, not a complete policy.
  The expression must evaluate to true for this binding to grant access.
  Quote expressions to protect them from shell interpretation, and use
  "has" before accessing optional fields.

  Omitting --condition creates an unconditional binding on create and
  preserves the existing condition on update. An explicitly empty value
  (--condition '') makes the binding unconditional. Expressions are sent
  unchanged; the Platform API validates them.

  GCP HCP exposes these fields:
    context.resourcePlural   Resource type: clusters, nodepools, roles, etc.
    context.resourceName     Target object's name, when available
    context.method           HTTP method: GET, POST, PUT, PATCH, DELETE
    context.spec             Target resource's public specification

  Cedar syntax:
    https://docs.cedarpolicy.com/policies/syntax-operators.html

Safety:
  Conditions restrict only this binding; independent grants may still
  allow access. Changes may take time to propagate.

  Keep a separate administrator grant. A condition excluding roles or
  rolebindings can prevent you from managing access or clearing it.`
