// Package permission defines the permission mode type and rule-parsing helpers
// shared across the agent and tools layers.
package permission

import "strings"

// Mode represents the permission enforcement mode for an agent session.
type Mode string

const (
	ModeDefault Mode = "default"
	ModePlan    Mode = "plan"
	ModeBypass  Mode = "bypass"
	ModeAuto    Mode = "auto"
)

// NormalizeMode returns m if non-empty, otherwise ModeDefault.
func NormalizeMode(m Mode) Mode {
	if m == "" {
		return ModeDefault
	}
	return m
}

// RuleValue is the parsed result of a tool rule spec like "Bash(pattern)"
// or a bare "ToolName".
type RuleValue struct {
	// Tool is the tool name portion (before the first '(').
	Tool string
	// Pattern is the argument string inside the parentheses, if any.
	Pattern string
	// HasArgs is true when parentheses were present in the spec.
	HasArgs bool
}

// ParseRuleValue parses a permission rule spec of the form "ToolName(args)"
// or plain "ToolName". Mirrors claude-code's parseRuleValue helper.
func ParseRuleValue(spec string) RuleValue {
	i := strings.IndexByte(spec, '(')
	if i < 0 {
		return RuleValue{Tool: strings.TrimSpace(spec)}
	}
	tool := strings.TrimSpace(spec[:i])
	j := strings.LastIndexByte(spec, ')')
	pattern := ""
	if j > i {
		pattern = strings.TrimSpace(spec[i+1 : j])
	}
	return RuleValue{Tool: tool, Pattern: pattern, HasArgs: true}
}

// ParseCommaList splits a comma-separated string into trimmed, non-empty tokens.
func ParseCommaList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
