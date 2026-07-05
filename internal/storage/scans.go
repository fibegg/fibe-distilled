package storage

import (
	"database/sql"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// scanner is the shared interface for sql.Row and sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanMarquee decodes one Marquee row into the API domain shape.
func scanMarquee(row scanner) (domain.Marquee, error) {
	var m domain.Marquee
	var domains, tlsSource, acme, platform sql.NullString
	var https int
	var created, updated string
	err := row.Scan(&m.ID, &m.Name, &m.Host, &m.Port, &m.User, &m.SSHPrivateKey, &domains, &https, &tlsSource, &acme, &platform, &m.Status, &created, &updated)
	if err != nil {
		return m, err
	}
	m.DomainsInput = stringPtr(domains)
	m.HTTPSEnabled = new(https == 1)
	m.TLSCertificateSource = stringPtr(tlsSource)
	m.AcmeEmail = stringPtr(acme)
	m.BuildPlatform = stringPtr(platform)
	m.RuntimeLaunchable = true
	m.ChatLaunchable = m.Status != "error"
	if m.CreatedAt, err = parseStoredTime("marquees.created_at", created); err != nil {
		return m, err
	}
	if m.UpdatedAt, err = parseStoredTime("marquees.updated_at", updated); err != nil {
		return m, err
	}
	return m, nil
}

// scanPlayspec decodes one Playspec row and fixed stateless compatibility fields.
func scanPlayspec(row scanner) (domain.Playspec, error) {
	var p domain.Playspec
	var id int64
	var desc sql.NullString
	var servicesJSON string
	var created, updated string
	err := row.Scan(&id, &p.Name, &desc, &p.BaseComposeYAML, &servicesJSON, &created, &updated)
	if err != nil {
		return p, err
	}
	p.ID = &id
	p.Description = stringPtr(desc)
	p.PersistVolumes = new(false)
	p.Locked = new(false)
	count := int64(0)
	p.PlaygroundCount = &count
	ct, err := parseStoredTime("playspecs.created_at", created)
	if err != nil {
		return p, err
	}
	ut, err := parseStoredTime("playspecs.updated_at", updated)
	if err != nil {
		return p, err
	}
	p.CreatedAt = &ct
	p.UpdatedAt = &ut
	if err := decodeStoredJSON(servicesJSON, "playspecs.services_json", &p.Services); err != nil {
		return p, err
	}
	return p, nil
}

// scanAsync decodes one async operation row.
func scanAsync(row scanner) (domain.AsyncOperation, error) {
	var op domain.AsyncOperation
	var payloadJSON string
	var errJSON sql.NullString
	var created, updated string
	err := row.Scan(&op.ID, &op.Status, &payloadJSON, &errJSON, &created, &updated)
	if err != nil {
		return op, err
	}
	if err := decodeStoredJSON(payloadJSON, "async_operations.payload_json", &op.Payload); err != nil {
		return op, err
	}
	if errJSON.Valid {
		var apiErr domain.APIError
		if err := decodeStoredJSON(errJSON.String, "async_operations.error_json", &apiErr); err != nil {
			return op, err
		}
		op.Error = &apiErr
	}
	op.StatusURL = "/api/async_requests/" + op.ID
	if op.CreatedAt, err = parseStoredTime("async_operations.created_at", created); err != nil {
		return op, err
	}
	if op.UpdatedAt, err = parseStoredTime("async_operations.updated_at", updated); err != nil {
		return op, err
	}
	return op, nil
}
