package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/fibegg/fibe-distilled/internal/domain"
)

const (
	// defaultAddr is the fixed HTTP listen address. It intentionally has no env override.
	defaultAddr = ":2402"
	// defaultDBPath is the SQLite path used when no database path is configured.
	defaultDBPath = "data/fibe-distilled.sqlite3"
	// defaultDataDir is the local runtime-data directory used when none is configured.
	defaultDataDir = "data"
)

// Config is the fully parsed process configuration used by main.
type Config struct {
	// Addr is the HTTP listen address.
	Addr string
	// DBPath is the SQLite database path or DSN.
	DBPath string
	// DataDir is the local directory for runtime state.
	DataDir string
	// APIToken is the static bearer token for /api/*.
	APIToken string
	// GitHubTok is the optional process-wide GitHub token.
	GitHubTok string
	// GitHubWebhookSecret verifies manually configured GitHub push webhooks.
	GitHubWebhookSecret string
	// GitHubWebhookAutoRollout deploys successful webhook-built images immediately.
	GitHubWebhookAutoRollout bool
	// DockerHubUsername is the optional pull/build registry username.
	DockerHubUsername string
	// DockerHubToken is the optional pull/build registry token.
	DockerHubToken string
	// PlayguardInterval controls the reconciliation loop cadence.
	PlayguardInterval time.Duration
	// Marquee is the startup-configured runtime host.
	Marquee MarqueeConfig
}

// MarqueeConfig is the startup-only Marquee configuration from environment.
type MarqueeConfig struct {
	// Name is the fixed Marquee display and lookup name.
	Name string
	// Domain is the single root domain routed by Traefik.
	Domain string
	// User is a local compatibility field exposed through the Marquee API.
	User string
	// Host is a local compatibility field exposed through the Marquee API.
	Host string
	// Port is a local compatibility field exposed through the Marquee API.
	Port int
	// AcmeEmail is the Let's Encrypt account email.
	AcmeEmail string
	// BuildPlatform is the optional Docker build platform.
	BuildPlatform string
}

// rawEnvConfig is the direct environment-variable shape parsed by caarlos0/env.
type rawEnvConfig struct {
	DBPath                   string `env:"FIBE_DB_PATH"`
	DataDir                  string `env:"FIBE_DATA_DIR"`
	APIToken                 string `env:"FIBE_API_KEY"`
	GitHubTok                string `env:"GITHUB_TOKEN"`
	GitHubWebhookSecret      string `env:"GITHUB_WEBHOOK_SECRET"`
	GitHubWebhookAutoRollout bool   `env:"FIBE_GITHUB_WEBHOOK_AUTO_ROLLOUT"`
	DockerHubUsername        string `env:"DOCKERHUB_USERNAME"`
	DockerHubToken           string `env:"DOCKERHUB_TOKEN"`
	PlayguardIntervalSeconds int    `env:"FIBE_PLAYGUARD_INTERVAL_SECONDS" envDefault:"30"`
	RootDomain               string `env:"FIBE_ROOT_DOMAIN"`
	AcmeEmail                string `env:"FIBE_ACME_EMAIL"`
	BuildPlatform            string `env:"FIBE_BUILD_PLATFORM"`
}

// ToDomain converts startup Marquee configuration into the persisted domain model.
func (m MarqueeConfig) ToDomain() domain.Marquee {
	name := strings.TrimSpace(m.Name)
	if name == "" {
		name = "default"
	}
	https := true
	tlsSource := "automatic"
	domainValue := strings.TrimSpace(m.Domain)
	acmeEmail := strings.TrimSpace(m.AcmeEmail)
	buildPlatform := strings.TrimSpace(m.BuildPlatform)
	out := domain.Marquee{
		Name:                 name,
		Host:                 strings.TrimSpace(m.Host),
		Port:                 m.Port,
		User:                 strings.TrimSpace(m.User),
		SSHPrivateKey:        "",
		DomainsInput:         &domainValue,
		HTTPSEnabled:         &https,
		TLSCertificateSource: &tlsSource,
		AcmeEmail:            &acmeEmail,
		Status:               "active",
	}
	if buildPlatform != "" {
		out.BuildPlatform = &buildPlatform
	}
	return out
}

// Load reads environment variables, validates them, and prepares local data paths.
func Load() (Config, error) {
	raw, err := loadRawEnv()
	if err != nil {
		return Config{}, err
	}
	cfg, err := configFromRawEnv(raw)
	if err != nil {
		return cfg, err
	}
	return cfg, prepareLocalPaths(cfg)
}

// loadRawEnv parses environment variables without applying domain validation.
func loadRawEnv() (rawEnvConfig, error) {
	var raw rawEnvConfig
	err := env.Parse(&raw)
	return raw, err
}

// configFromRawEnv validates and normalizes the parsed environment.
func configFromRawEnv(raw rawEnvConfig) (Config, error) {
	marquee, err := loadMarqueeConfig(raw.RootDomain, raw.AcmeEmail, raw.BuildPlatform)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Addr:                     defaultAddr,
		DBPath:                   withDefault(raw.DBPath, defaultDBPath),
		DataDir:                  withDefault(raw.DataDir, defaultDataDir),
		APIToken:                 strings.TrimSpace(raw.APIToken),
		GitHubTok:                strings.TrimSpace(raw.GitHubTok),
		GitHubWebhookSecret:      strings.TrimSpace(raw.GitHubWebhookSecret),
		GitHubWebhookAutoRollout: raw.GitHubWebhookAutoRollout,
		DockerHubUsername:        strings.TrimSpace(raw.DockerHubUsername),
		DockerHubToken:           strings.TrimSpace(raw.DockerHubToken),
		Marquee:                  marquee,
	}
	cfg.PlayguardInterval, err = playguardInterval(raw.PlayguardIntervalSeconds)
	if err != nil {
		return cfg, err
	}
	if cfg.APIToken == "" {
		return cfg, errors.New("FIBE_API_KEY is required")
	}
	// GITHUB_TOKEN is optional: it is only needed for GitHub repo write-access
	// checks and private source sync/build labels in caller-supplied Compose.
	// Image-only playgrounds — the core use case — work without it.
	return cfg, nil
}

// prepareLocalPaths creates local directories required before the server starts.
func prepareLocalPaths(cfg Config) error {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	if dbDir := databaseDir(cfg.DBPath); dbDir != "" {
		if err := os.MkdirAll(dbDir, 0o700); err != nil {
			return fmt.Errorf("create database dir: %w", err)
		}
	}
	return nil
}
