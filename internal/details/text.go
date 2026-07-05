package details

import (
	"regexp"
	"sort"
	"strings"
)

// ansiPattern matches terminal color/control sequences emitted by Docker tools.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[mGKHF]`)

// stripANSI removes terminal color/control sequences from command output.
func stripANSI(text string) string {
	return ansiPattern.ReplaceAllString(text, "")
}

// nonEmptyLines returns trimmed nonblank lines from command output.
func nonEmptyLines(text string) []string {
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, line := range lines {
		if line != "" {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}

// matchGroup returns the first capture group for a precompiled pattern.
func matchGroup(text string, pattern *regexp.Regexp) string {
	match := pattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

// uniqueNames returns service names once, longest first for suffix matching.
func uniqueNames(names []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// dedupe returns items in order with repeated values removed.
func dedupe(items []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, item := range items {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
