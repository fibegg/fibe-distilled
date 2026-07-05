package service

import (
	"regexp"
	"strings"
)

// shellOperatorPattern detects command strings that need shell execution.
var shellOperatorPattern = regexp.MustCompile(`&&|\|\||[;|><]|\$\(`)

// normalizeStartCommand converts API/label command values into Compose command form.
func normalizeStartCommand(raw any) (any, bool) {
	switch typed := raw.(type) {
	case nil:
		return nil, false
	case string:
		return normalizeStartCommandString(typed)
	case []string:
		return normalizeStartCommandStringSlice(typed)
	case []any:
		return normalizeStartCommandAnySlice(typed)
	default:
		return nil, false
	}
}

// normalizeStartCommandString wraps shell-like strings in sh -c.
func normalizeStartCommandString(command string) (any, bool) {
	if strings.TrimSpace(command) == "" {
		return nil, false
	}
	if shellOperatorPattern.MatchString(command) {
		return []string{"sh", "-c", command}, true
	}
	return command, true
}

// normalizeStartCommandStringSlice preserves argv-style command values.
func normalizeStartCommandStringSlice(command []string) (any, bool) {
	if !validStartCommandArgs(command) {
		return nil, false
	}
	return command, true
}

// normalizeStartCommandAnySlice stringifies loose JSON array command values.
func normalizeStartCommandAnySlice(command []any) (any, bool) {
	if len(command) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(command))
	for _, item := range command {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, text)
	}
	return normalizeStartCommandStringSlice(out)
}

// validStartCommandArgs rejects empty argv-style command values.
func validStartCommandArgs(command []string) bool {
	if len(command) == 0 {
		return false
	}
	for _, arg := range command {
		if strings.TrimSpace(arg) == "" {
			return false
		}
	}
	return true
}

// injectStartCommand writes a service command from Fibe metadata.
func injectStartCommand(services map[string]any, summary Summary) {
	if summary.StartCommand == "" {
		return
	}
	raw, ok := services[summary.Name].(map[string]any)
	if !ok {
		return
	}
	if command, ok := normalizeStartCommand(summary.StartCommand); ok {
		raw["command"] = command
	}
}
