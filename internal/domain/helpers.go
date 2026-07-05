package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// dockerBuildArgNamePattern matches Docker ARG variable names fibe-distilled accepts.
var dockerBuildArgNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// RandomID returns a compact prefixed random identifier.
func RandomID(prefix string, byteCount int) string {
	if byteCount < 1 {
		byteCount = 1
	}
	b := make([]byte, byteCount)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(b)
}

// FirstDomainFromInput selects the first configured Marquee root domain.
func FirstDomainFromInput(input *string) string {
	if input == nil {
		return ""
	}
	for _, raw := range strings.FieldsFunc(*input, marqueeDomainSeparator) {
		if domain := strings.TrimSpace(raw); domain != "" {
			return domain
		}
	}
	return ""
}

// ParseDockerBuildArgs parses and normalizes Fibe's comma-separated build args.
func ParseDockerBuildArgs(raw string) ([]string, bool) {
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		normalized := normalizeDockerBuildArgPart(part)
		if !normalized.valid {
			return nil, false
		}
		if normalized.effective {
			out = append(out, normalized.value)
		}
	}
	return out, true
}

// NormalizeDockerBuildArg returns the canonical CLI build-arg entry.
func NormalizeDockerBuildArg(raw string) (string, bool) {
	normalized := normalizeDockerBuildArgPart(raw)
	return normalized.value, normalized.effective && normalized.valid
}

// dockerBuildArgPart carries one normalized build-arg token and its status.
type dockerBuildArgPart struct {
	value     string
	effective bool
	valid     bool
}

// normalizeDockerBuildArgPart normalizes one Fibe build-arg token.
func normalizeDockerBuildArgPart(raw string) dockerBuildArgPart {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return dockerBuildArgPart{valid: true}
	}
	key, value, hasValue := strings.Cut(raw, "=")
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" {
		return dockerBuildArgPart{valid: true}
	}
	if !dockerBuildArgNamePattern.MatchString(key) {
		return dockerBuildArgPart{}
	}
	if hasValue {
		return dockerBuildArgPart{value: key + "=" + value, effective: true, valid: true}
	}
	return dockerBuildArgPart{value: key, effective: true, valid: true}
}

// marqueeDomainSeparator reports delimiters accepted in stored domain input.
func marqueeDomainSeparator(r rune) bool {
	return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
}
