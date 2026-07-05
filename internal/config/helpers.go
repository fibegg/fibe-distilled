package config

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

const (
	// maxDNSNameLength is the RFC DNS hostname length limit.
	maxDNSNameLength = 253
	// maxDNSLabelLength is the RFC DNS label length limit.
	maxDNSLabelLength = 63
	// maxPlayguardIntervalSeconds is the largest second value that fits time.Duration.
	maxPlayguardIntervalSeconds = int64(1<<63-1) / int64(time.Second)
)

// databaseDir extracts a filesystem directory from a plain or file: SQLite DSN.
func databaseDir(dbPath string) string {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" || dbPath == ":memory:" {
		return ""
	}
	if !strings.HasPrefix(dbPath, "file:") {
		return filepath.Dir(dbPath)
	}
	name, ok := sqliteFileDSNPath(dbPath)
	if !ok {
		return ""
	}
	return filepath.Dir(name)
}

// sqliteFileDSNPath extracts a filesystem path from a non-memory SQLite file DSN.
func sqliteFileDSNPath(dbPath string) (string, bool) {
	name := strings.TrimPrefix(dbPath, "file:")
	query := ""
	if dsnPath, dsnQuery, ok := strings.Cut(name, "?"); ok {
		name = dsnPath
		query = dsnQuery
	}
	if name == "" || strings.HasPrefix(name, ":") || sqliteQueryUsesMemory(query) {
		return "", false
	}
	return name, true
}

// sqliteQueryUsesMemory reports whether a SQLite file: DSN is memory-backed.
func sqliteQueryUsesMemory(query string) bool {
	values, err := url.ParseQuery(query)
	if err != nil {
		return false
	}
	return strings.EqualFold(values.Get("mode"), "memory")
}

// withDefault trims a value and returns the fallback when the result is blank.
func withDefault(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

// playguardInterval converts configured seconds without overflowing.
func playguardInterval(seconds int) (time.Duration, error) {
	if seconds <= 0 || int64(seconds) > maxPlayguardIntervalSeconds {
		return 0, errors.New("FIBE_PLAYGUARD_INTERVAL_SECONDS must be positive and within range")
	}
	return time.Duration(seconds) * time.Second, nil
}

// loadMarqueeConfig validates the single local startup-configured Marquee.
func loadMarqueeConfig(domainValue, acmeEmail, buildPlatform string) (MarqueeConfig, error) {
	domainValue = strings.TrimSpace(domainValue)
	acmeEmail = strings.TrimSpace(acmeEmail)
	buildPlatform = strings.TrimSpace(buildPlatform)
	if err := validateMarqueeDomain(domainValue); err != nil {
		return MarqueeConfig{}, err
	}
	if acmeEmail == "" {
		return MarqueeConfig{}, errors.New("FIBE_ACME_EMAIL is required")
	}
	if err := validateACMEEmail(acmeEmail); err != nil {
		return MarqueeConfig{}, err
	}
	platform, err := domain.ParseBuildPlatform(buildPlatform)
	if err != nil {
		return MarqueeConfig{}, fmt.Errorf("FIBE_BUILD_PLATFORM: %w", err)
	}
	buildPlatform = platform.String()
	return MarqueeConfig{
		Name:          "default",
		Domain:        domainValue,
		User:          "local",
		Host:          "localhost",
		Port:          0,
		AcmeEmail:     acmeEmail,
		BuildPlatform: buildPlatform,
	}, nil
}

// validateACMEEmail checks that Let's Encrypt can use the configured address.
func validateACMEEmail(email string) error {
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return errors.New("FIBE_ACME_EMAIL must be a plain email address")
	}
	_, domainPart, ok := strings.Cut(parsed.Address, "@")
	if !ok || !validEmailDomain(domainPart) {
		return errors.New("FIBE_ACME_EMAIL must use a DNS hostname domain")
	}
	return nil
}

// validEmailDomain checks the DNS hostname portion of an ACME email.
func validEmailDomain(domainValue string) bool {
	if len(domainValue) > maxDNSNameLength || !strings.Contains(domainValue, ".") {
		return false
	}
	for label := range strings.SplitSeq(domainValue, ".") {
		if !validDNSLabel(label) {
			return false
		}
	}
	return true
}

// validateMarqueeDomain checks the single root domain used for routing and ACME.
func validateMarqueeDomain(domainValue string) error {
	if err := validateMarqueeDomainShape(domainValue); err != nil {
		return err
	}
	return validateMarqueeDomainLabels(domainValue)
}

// validateMarqueeDomainShape checks whole-domain syntax before label parsing.
func validateMarqueeDomainShape(domainValue string) error {
	switch {
	case domainValue == "":
		return errors.New("FIBE_ROOT_DOMAIN is required")
	case strings.ContainsAny(domainValue, ", \t\r\n"):
		return errors.New("FIBE_ROOT_DOMAIN must contain exactly one domain")
	case strings.Contains(domainValue, "://") || strings.ContainsAny(domainValue, `/\`):
		return errors.New("FIBE_ROOT_DOMAIN must be a hostname, not a URL")
	case strings.Contains(domainValue, ":"):
		return errors.New("FIBE_ROOT_DOMAIN must not include a port")
	case len(domainValue) > maxDNSNameLength:
		return errors.New("FIBE_ROOT_DOMAIN is too long")
	case !strings.Contains(domainValue, "."):
		return errors.New("FIBE_ROOT_DOMAIN must be a DNS hostname with at least one dot")
	default:
		return nil
	}
}

// validateMarqueeDomainLabels checks each DNS label in the Marquee domain.
func validateMarqueeDomainLabels(domainValue string) error {
	for label := range strings.SplitSeq(domainValue, ".") {
		if !validDNSLabel(label) {
			return errors.New("FIBE_ROOT_DOMAIN has an invalid DNS label")
		}
	}
	return nil
}

// validDNSLabel checks one DNS hostname label.
func validDNSLabel(label string) bool {
	if label == "" || len(label) > maxDNSLabelLength || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
		return false
	}
	for _, r := range label {
		if !isDNSLabelChar(r) {
			return false
		}
	}
	return true
}

// isDNSLabelChar reports whether a rune is valid inside a DNS label.
func isDNSLabelChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-'
}
