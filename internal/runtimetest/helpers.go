package runtimetest

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
)

// exactResult returns a configured exact command result or error.
func (f *FakeExecutor) exactResult(command string) (runtime.CommandResult, bool, error) {
	if f.Errors != nil && f.Errors[command] != nil {
		return f.Results[command], true, f.Errors[command]
	}
	if f.Results != nil {
		if result, ok := f.Results[command]; ok {
			return result, true, nil
		}
	}
	return runtime.CommandResult{}, false, nil
}

// containsResult returns the first configured substring command result or error.
func (f *FakeExecutor) containsResult(command string) (runtime.CommandResult, bool, error) {
	for fragment, err := range f.ErrorContains {
		if strings.Contains(command, fragment) {
			return f.ResultContains[fragment], true, err
		}
	}
	for fragment, result := range f.ResultContains {
		if strings.Contains(command, fragment) {
			return result, true, nil
		}
	}
	return runtime.CommandResult{}, false, nil
}

// MustRemoteCheckoutPath returns a valid remote checkout path for tests.
func MustRemoteCheckoutPath(t testing.TB, project string, raw string) runtime.RemoteCheckoutPath {
	return mustRuntimeValue(t, "remote checkout path", func() (runtime.RemoteCheckoutPath, error) {
		return runtime.NewRemoteCheckoutPath(project, raw)
	})
}

// MustRelativeDockerfilePath returns a valid relative Dockerfile path for tests.
func MustRelativeDockerfilePath(t testing.TB, raw string) runtime.RelativeDockerfilePath {
	return mustRuntimeValue(t, "relative dockerfile path", func() (runtime.RelativeDockerfilePath, error) {
		return runtime.NewRelativeDockerfilePath(raw)
	})
}

// InsertLegacyMarquee inserts a Marquee row outside production store APIs.
func InsertLegacyMarquee(ctx context.Context, t testing.TB, dbPath string, m domain.Marquee) domain.Marquee {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy marquee db: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close legacy marquee db: %v", err)
		}
	}()
	if m.Port == 0 {
		m.Port = 22
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `INSERT INTO marquees (name,host,port,user,ssh_private_key,domains_input,https_enabled,tls_certificate_source,acme_email,build_platform,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.Name, m.Host, m.Port, m.User, m.SSHPrivateKey, nullableString(m.DomainsInput), boolInt(m.HTTPSEnabled), nullableString(m.TLSCertificateSource), nullableString(m.AcmeEmail), nullableString(m.BuildPlatform), m.Status, now, now)
	if err != nil {
		t.Fatalf("insert legacy marquee: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("legacy marquee id: %v", err)
	}
	m.ID = id
	return m
}

// boolInt converts an optional test boolean to a SQLite integer.
func boolInt(value *bool) int {
	if value != nil && *value {
		return 1
	}
	return 0
}

// mustRuntimeValue fails the test when a typed runtime constructor rejects input.
func mustRuntimeValue[T any](t testing.TB, label string, build func() (T, error)) T {
	t.Helper()
	value, err := build()
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	return value
}

// nullableString converts an optional test string to a database value.
func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
