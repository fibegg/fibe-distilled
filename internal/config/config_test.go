package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

func TestLoadRequiresLocalRuntimeEnv(t *testing.T) {
	t.Setenv("FIBE_API_KEY", "token")
	t.Setenv("FIBE_DB_PATH", filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	t.Setenv("FIBE_DATA_DIR", t.TempDir())

	if _, err := Load(); err == nil {
		t.Fatal("Load should require local runtime env")
	}
}

func TestLoadParsesLocalMarquee(t *testing.T) {
	dataDir := t.TempDir()
	setRequiredEnv(t, dataDir, "apps.example.com")
	t.Setenv("FIBE_BUILD_PLATFORM", "linux/amd64")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertParsedMarquee(t, cfg.Marquee)
	assertMarqueeDomainDefaults(t, cfg.Marquee.ToDomain())
}

func TestLoadTrimsStartupSecretsAndPaths(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "fibe-distilled.sqlite3")
	setRequiredEnv(t, " "+dataDir+"\n", " apps.example.com\n")
	t.Setenv("FIBE_API_KEY", " dev-token\n")
	t.Setenv("GITHUB_TOKEN", " ghp_token\n")
	t.Setenv("GITHUB_WEBHOOK_SECRET", " webhook-secret\n")
	t.Setenv("DOCKERHUB_USERNAME", " dock-user\n")
	t.Setenv("DOCKERHUB_TOKEN", " dock-token\n")
	t.Setenv("FIBE_DB_PATH", " "+dbPath+"\n")
	t.Setenv("FIBE_ACME_EMAIL", " ops@example.com\n")
	t.Setenv("FIBE_BUILD_PLATFORM", " linux/amd64\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertNormalizedStartupSecrets(t, cfg)
	assertNormalizedStartupPaths(t, cfg, dbPath, dataDir)
	assertNormalizedMarquee(t, cfg.Marquee)
}

func TestLoadUsesDefaultsForBlankOptionalStartupEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("FIBE_DB_PATH", " \n")
	t.Setenv("FIBE_DATA_DIR", " \n")
	t.Setenv("FIBE_API_KEY", "token")
	t.Setenv("FIBE_ROOT_DOMAIN", "apps.example.com")
	t.Setenv("FIBE_ACME_EMAIL", "ops@example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != defaultAddr || cfg.DBPath != defaultDBPath || cfg.DataDir != defaultDataDir {
		t.Fatalf("defaults not applied: addr=%q db=%q data=%q", cfg.Addr, cfg.DBPath, cfg.DataDir)
	}
}

func TestLoadIgnoresRemovedFibeDistilledEnvNames(t *testing.T) {
	t.Setenv("FIBE_DISTILLED_API_TOKEN", "old-token")
	t.Setenv("FIBE_DISTILLED_MARQUEE_DOMAIN", "apps.example.com")
	t.Setenv("FIBE_DISTILLED_ACME_EMAIL", "ops@example.com")
	t.Setenv("FIBE_DISTILLED_MARQUEE_CONNECTION", "deploy@example.com")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "FIBE_ROOT_DOMAIN") {
		t.Fatalf("Load error = %v, want new env validation", err)
	}
}

func TestLoadKeepsFixedAddress(t *testing.T) {
	setRequiredEnv(t, t.TempDir(), "apps.example.com")
	t.Setenv("FIBE_DISTILLED_ADDR", ":9999")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":2402" {
		t.Fatalf("addr = %q, want fixed :2402", cfg.Addr)
	}
}

func TestLoadParsesPlayguardInterval(t *testing.T) {
	t.Setenv("FIBE_API_KEY", "token")
	t.Setenv("FIBE_DB_PATH", filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	t.Setenv("FIBE_DATA_DIR", t.TempDir())
	t.Setenv("FIBE_PLAYGUARD_INTERVAL_SECONDS", "5")
	t.Setenv("FIBE_ROOT_DOMAIN", "apps.example.com")
	t.Setenv("FIBE_ACME_EMAIL", "ops@example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PlayguardInterval != 5*time.Second {
		t.Fatalf("playguard interval = %s, want 5s", cfg.PlayguardInterval)
	}
}

func TestLoadRejectsInvalidPlayguardIntervals(t *testing.T) {
	tests := []string{
		"0",
		"-1",
	}
	for _, seconds := range tests {
		t.Run(seconds, func(t *testing.T) {
			t.Setenv("FIBE_API_KEY", "token")
			t.Setenv("FIBE_DB_PATH", filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
			t.Setenv("FIBE_DATA_DIR", t.TempDir())
			t.Setenv("FIBE_PLAYGUARD_INTERVAL_SECONDS", seconds)
			t.Setenv("FIBE_ROOT_DOMAIN", "apps.example.com")
			t.Setenv("FIBE_ACME_EMAIL", "ops@example.com")

			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "FIBE_PLAYGUARD_INTERVAL_SECONDS") {
				t.Fatalf("Load error = %v, want playguard interval validation error", err)
			}
		})
	}
}

func TestPlayguardIntervalRejectsOverflow(t *testing.T) {
	if _, err := playguardInterval(int(maxPlayguardIntervalSeconds + 1)); err == nil {
		t.Fatal("playguardInterval should reject values that overflow time.Duration")
	}
	if got, err := playguardInterval(int(maxPlayguardIntervalSeconds)); err != nil || got <= 0 {
		t.Fatalf("playguardInterval should accept the maximum safe value, got %s err=%v", got, err)
	}
}

func TestLoadRejectsURLLikeRootDomains(t *testing.T) {
	tests := []struct {
		name       string
		domain     string
		wantErrSub string
	}{
		{name: "scheme", domain: "https://apps.example.com", wantErrSub: "hostname, not a URL"},
		{name: "path", domain: "apps.example.com/playgrounds", wantErrSub: "hostname, not a URL"},
		{name: "port", domain: "apps.example.com:443", wantErrSub: "must not include a port"},
		{name: "multiple domains", domain: "apps.example.com,apps2.example.com", wantErrSub: "exactly one domain"},
		{name: "single label", domain: "localhost", wantErrSub: "at least one dot"},
		{name: "empty label", domain: "apps..example.com", wantErrSub: "invalid DNS label"},
		{name: "leading hyphen label", domain: "-apps.example.com", wantErrSub: "invalid DNS label"},
		{name: "trailing hyphen label", domain: "apps-.example.com", wantErrSub: "invalid DNS label"},
		{name: "bad character", domain: "apps_example.com", wantErrSub: "invalid DNS label"},
		{name: "label too long", domain: strings.Repeat("a", 64) + ".example.com", wantErrSub: "invalid DNS label"},
		{name: "hostname too long", domain: strings.Join([]string{strings.Repeat("a", 63), strings.Repeat("b", 63), strings.Repeat("c", 63), strings.Repeat("d", 63)}, "."), wantErrSub: "too long"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("FIBE_API_KEY", "token")
			t.Setenv("FIBE_DB_PATH", filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
			t.Setenv("FIBE_DATA_DIR", t.TempDir())
			t.Setenv("FIBE_ROOT_DOMAIN", tt.domain)
			t.Setenv("FIBE_ACME_EMAIL", "ops@example.com")

			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("Load error = %v, want substring %q", err, tt.wantErrSub)
			}
		})
	}
}

func TestLoadRejectsInvalidACMEEmail(t *testing.T) {
	tests := []struct {
		email     string
		wantError string
	}{
		{email: "ops", wantError: "plain email address"},
		{email: "ops@example", wantError: "DNS hostname domain"},
		{email: "ops@bad_domain.example.com", wantError: "DNS hostname domain"},
		{email: "Ops <ops@example.com>", wantError: "plain email address"},
	}
	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			t.Setenv("FIBE_API_KEY", "token")
			t.Setenv("FIBE_DB_PATH", filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
			t.Setenv("FIBE_DATA_DIR", t.TempDir())
			t.Setenv("FIBE_ROOT_DOMAIN", "apps.example.com")
			t.Setenv("FIBE_ACME_EMAIL", tt.email)

			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Load error = %v, want substring %q", err, tt.wantError)
			}
		})
	}
}

func TestLoadRejectsInvalidBuildPlatform(t *testing.T) {
	tests := []string{
		"linux",
		"amd64",
		"linux/amd64/extra/value",
		"linux/amd64 extra",
		"linux/amd_64",
		"linux/x86_64",
		"linux/arm64/v8",
	}
	for _, platform := range tests {
		t.Run(platform, func(t *testing.T) {
			t.Setenv("FIBE_API_KEY", "token")
			t.Setenv("FIBE_DB_PATH", filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
			t.Setenv("FIBE_DATA_DIR", t.TempDir())
			t.Setenv("FIBE_ROOT_DOMAIN", "apps.example.com")
			t.Setenv("FIBE_ACME_EMAIL", "ops@example.com")
			t.Setenv("FIBE_BUILD_PLATFORM", platform)

			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "FIBE_BUILD_PLATFORM") {
				t.Fatalf("Load error = %v, want build platform validation error", err)
			}
		})
	}
}

func TestLoadAcceptsSupportedBuildPlatform(t *testing.T) {
	tests := map[string]string{
		"linux/amd64":    "linux/amd64",
		" linux/arm64\n": "linux/arm64",
	}
	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			setRequiredEnv(t, t.TempDir(), "apps.example.com")
			t.Setenv("FIBE_BUILD_PLATFORM", raw)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Marquee.BuildPlatform != want {
				t.Fatalf("build platform = %q, want %q", cfg.Marquee.BuildPlatform, want)
			}
		})
	}
}

func TestLoadPreparesSQLiteFileDSNDirectory(t *testing.T) {
	parent := t.TempDir()
	dbDir := filepath.Join(parent, "db")
	t.Setenv("FIBE_API_KEY", "token")
	t.Setenv("FIBE_DB_PATH", "file:"+filepath.Join(dbDir, "fibe-distilled.sqlite3")+"?cache=shared")
	t.Setenv("FIBE_DATA_DIR", filepath.Join(parent, "data"))
	t.Setenv("FIBE_ROOT_DOMAIN", "apps.example.com")
	t.Setenv("FIBE_ACME_EMAIL", "ops@example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.HasPrefix(cfg.DBPath, "file:") {
		t.Fatalf("expected file DSN to be preserved, got %q", cfg.DBPath)
	}
	if info, err := os.Stat(dbDir); err != nil || !info.IsDir() {
		t.Fatalf("expected real SQLite DSN directory to be created, info=%#v err=%v", info, err)
	}
}

func TestDatabaseDirForSQLiteDSNs(t *testing.T) {
	tests := []struct {
		name   string
		dbPath string
		want   string
	}{
		{name: "plain path", dbPath: filepath.Join("data", "fibe-distilled.sqlite3"), want: "data"},
		{name: "file dsn", dbPath: "file:" + filepath.Join("data", "fibe-distilled.sqlite3") + "?cache=shared", want: "data"},
		{name: "plain memory dsn", dbPath: ":memory:", want: ""},
		{name: "memory dsn", dbPath: "file::memory:?cache=shared", want: ""},
		{name: "named memory dsn", dbPath: "file:fibe-distilled?mode=memory&cache=shared", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := databaseDir(tt.dbPath); got != tt.want {
				t.Fatalf("databaseDir(%q) = %q, want %q", tt.dbPath, got, tt.want)
			}
		})
	}
}

func setRequiredEnv(t *testing.T, dataDir string, rootDomain string) {
	t.Helper()
	t.Setenv("FIBE_API_KEY", "token")
	t.Setenv("FIBE_DB_PATH", filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	t.Setenv("FIBE_DATA_DIR", dataDir)
	t.Setenv("FIBE_ROOT_DOMAIN", rootDomain)
	t.Setenv("FIBE_ACME_EMAIL", "ops@example.com")
}

func assertParsedMarquee(t *testing.T, got MarqueeConfig) {
	t.Helper()
	if got.Domain != "apps.example.com" || got.User != "local" || got.Host != "localhost" || got.Port != 0 {
		t.Fatalf("unexpected marquee config: %#v", got)
	}
}

func assertMarqueeDomainDefaults(t *testing.T, got domain.Marquee) {
	t.Helper()
	if got.HTTPSEnabled == nil || !*got.HTTPSEnabled || got.AcmeEmail == nil || *got.AcmeEmail != "ops@example.com" {
		t.Fatalf("configured marquee should force HTTPS and ACME email: %#v", got)
	}
	if got.BuildPlatform == nil || *got.BuildPlatform != "linux/amd64" {
		t.Fatalf("build platform not propagated: %#v", got)
	}
	if got.Host != "localhost" || got.Port != 0 || got.User != "local" || got.SSHPrivateKey != "" {
		t.Fatalf("local marquee compatibility fields not applied: %#v", got)
	}
}

func assertNormalizedStartupSecrets(t *testing.T, got Config) {
	t.Helper()
	if got.APIToken != "dev-token" || got.GitHubTok != "ghp_token" ||
		got.GitHubWebhookSecret != "webhook-secret" ||
		got.DockerHubUsername != "dock-user" || got.DockerHubToken != "dock-token" {
		t.Fatal("startup credentials were not normalized")
	}
}

func assertNormalizedStartupPaths(t *testing.T, got Config, dbPath string, dataDir string) {
	t.Helper()
	if got.Addr != ":2402" || got.DBPath != dbPath || got.DataDir != dataDir {
		t.Fatalf("startup address or data paths were not normalized: addr=%q db=%q data=%q", got.Addr, got.DBPath, got.DataDir)
	}
}

func assertNormalizedMarquee(t *testing.T, got MarqueeConfig) {
	t.Helper()
	if got.Domain != "apps.example.com" || got.AcmeEmail != "ops@example.com" ||
		got.BuildPlatform != "linux/amd64" {
		t.Fatal("marquee config was not normalized")
	}
}
