// Package route decides which rule handles a webhook delivery. Matchers
// compare case-insensitive wildcard patterns (* and ?) against a value found
// by dotted path in the payload JSON, e.g. "title" or
// "commonLabels.namespace".
package route

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/trondhindenes/caramba/internal/model"
)

// FirstMatch returns the first rule whose matchers all match the raw
// payload, or nil when none does.
func FirstMatch(rules []*model.Rule, raw []byte) (*model.Rule, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decoding payload: %w", err)
	}
	for _, r := range rules {
		if Matches(r, doc) {
			return r, nil
		}
	}
	return nil, nil
}

// Matches reports whether every matcher of the rule matches the decoded
// payload. A rule without matchers matches everything.
func Matches(r *model.Rule, doc any) bool {
	for _, m := range r.Matchers {
		if !matchOne(m, doc) {
			return false
		}
	}
	return true
}

func matchOne(m model.Matcher, doc any) bool {
	re, err := Glob(m.Pattern)
	if err != nil {
		return false
	}
	// "title" needs no special case: it is a top-level payload key.
	for _, v := range lookup(doc, strings.Split(m.Field, ".")) {
		if s, ok := scalarString(v); ok && re.MatchString(s) {
			return true
		}
	}
	return false
}

// lookup resolves a dotted path, fanning out across arrays so that
// "alerts.labels.pod" yields the pod label of every alert in the group.
func lookup(v any, path []string) []any {
	if arr, ok := v.([]any); ok {
		var out []any
		for _, item := range arr {
			out = append(out, lookup(item, path)...)
		}
		return out
	}
	if len(path) == 0 {
		return []any{v}
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	next, ok := obj[path[0]]
	if !ok {
		return nil
	}
	return lookup(next, path[1:])
}

func scalarString(v any) (string, bool) {
	switch v := v.(type) {
	case string:
		return v, true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(v), true
	}
	return "", false
}

// Glob compiles a case-insensitive wildcard pattern (* and ?) that must
// match the whole string.
func Glob(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("(?is)^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// ParseMatchers reads matchers written one per line as "field = pattern".
// Blank lines and lines starting with # are ignored.
func ParseMatchers(text string) ([]model.Matcher, error) {
	matchers := []model.Matcher{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		field, pattern, ok := strings.Cut(line, "=")
		field, pattern = strings.TrimSpace(field), strings.TrimSpace(pattern)
		if !ok || field == "" || pattern == "" {
			return nil, fmt.Errorf("matcher line %d: want \"field = pattern\", got %q", i+1, line)
		}
		if strings.Contains(field, "..") || strings.HasPrefix(field, ".") || strings.HasSuffix(field, ".") {
			return nil, fmt.Errorf("matcher line %d: invalid field path %q", i+1, field)
		}
		matchers = append(matchers, model.Matcher{Field: field, Pattern: pattern})
	}
	return matchers, nil
}

// FormatMatchers is the inverse of ParseMatchers, for the rule editor.
func FormatMatchers(ms []model.Matcher) string {
	lines := make([]string, len(ms))
	for i, m := range ms {
		lines[i] = m.Field + " = " + m.Pattern
	}
	return strings.Join(lines, "\n")
}
