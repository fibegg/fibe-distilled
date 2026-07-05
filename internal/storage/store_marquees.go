package storage

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// createMarquee inserts a Marquee row for configured startup upsert.
func (s *DB) createMarquee(ctx context.Context, m domain.Marquee) (domain.Marquee, error) {
	now := time.Now().UTC()
	m.CreatedAt = now
	m.UpdatedAt = now
	normalizeMarqueeForStorage(&m)
	res, err := s.db.ExecContext(ctx, `INSERT INTO marquees (name,host,port,user,ssh_private_key,domains_input,https_enabled,tls_certificate_source,acme_email,build_platform,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.Name, m.Host, m.Port, m.User, m.SSHPrivateKey, nullableString(m.DomainsInput), boolValue(m.HTTPSEnabled), nullableString(m.TLSCertificateSource), nullableString(m.AcmeEmail), nullableString(m.BuildPlatform), m.Status, encodeTime(now), encodeTime(now))
	if err != nil {
		return m, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return m, err
	}
	m.ID = id
	return m, nil
}

// EnsureConfiguredMarquee upserts the startup-configured Marquee row.
func (s *DB) EnsureConfiguredMarquee(ctx context.Context, m domain.Marquee) (domain.Marquee, error) {
	if strings.TrimSpace(m.Name) == "" {
		m.Name = ConfiguredMarqueeName
	}
	m.Status = "active"
	normalizeMarqueeForStorage(&m)
	existing, err := s.GetMarquee(ctx, m.Name)
	if errors.Is(err, ErrNotFound) {
		return s.createMarquee(ctx, m)
	}
	if err != nil {
		return domain.Marquee{}, err
	}
	m.ID = existing.ID
	m.CreatedAt = existing.CreatedAt
	return s.saveMarquee(ctx, m)
}

// GetMarquee fetches a Marquee by ID or name.
func (s *DB) GetMarquee(ctx context.Context, identifier string) (domain.Marquee, error) {
	where, arg := identifierWhere(identifier)
	return queryOne(ctx, s.db, `SELECT id,name,host,port,user,ssh_private_key,domains_input,https_enabled,tls_certificate_source,acme_email,build_platform,status,created_at,updated_at FROM marquees WHERE `+where, arg, scanMarquee)
}

// GetRuntimeMarquee returns the Marquee allowed for runtime host operations.
func (s *DB) GetRuntimeMarquee(ctx context.Context) (domain.Marquee, bool, error) {
	configured, err := s.GetMarquee(ctx, ConfiguredMarqueeName)
	if err == nil {
		return configured, true, nil
	}
	if errors.Is(err, ErrNotFound) {
		return domain.Marquee{}, false, nil
	}
	return domain.Marquee{}, false, err
}

// saveMarquee updates an existing Marquee row.
func (s *DB) saveMarquee(ctx context.Context, m domain.Marquee) (domain.Marquee, error) {
	m.UpdatedAt = time.Now().UTC()
	normalizeMarqueeForStorage(&m)
	res, err := s.db.ExecContext(ctx, `UPDATE marquees SET name=?,host=?,port=?,user=?,ssh_private_key=?,domains_input=?,https_enabled=?,tls_certificate_source=?,acme_email=?,build_platform=?,status=?,updated_at=? WHERE id=?`,
		m.Name, m.Host, m.Port, m.User, m.SSHPrivateKey, nullableString(m.DomainsInput), boolValue(m.HTTPSEnabled), nullableString(m.TLSCertificateSource), nullableString(m.AcmeEmail), nullableString(m.BuildPlatform), m.Status, encodeTime(m.UpdatedAt), m.ID)
	return m, requireRowsAffected(res, err)
}

// normalizeMarqueeForStorage enforces fibe-distilled's startup-configured Marquee invariants.
func normalizeMarqueeForStorage(m *domain.Marquee) {
	https := true
	m.HTTPSEnabled = &https
	if m.DomainsInput != nil && strings.TrimSpace(*m.DomainsInput) != "" {
		if m.TLSCertificateSource == nil || strings.TrimSpace(*m.TLSCertificateSource) == "" {
			tlsSource := "automatic"
			m.TLSCertificateSource = &tlsSource
		}
	}
}
