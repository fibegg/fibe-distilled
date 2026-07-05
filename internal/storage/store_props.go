package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	compose "github.com/fibegg/fibe-distilled/internal/composefile"
	servicepkg "github.com/fibegg/fibe-distilled/internal/composefile/service"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/git"
	"gopkg.in/yaml.v3"
)

// ListProps returns all Props ordered by insertion.
func (s *DB) ListProps(ctx context.Context) ([]domain.Prop, error) {
	return queryRows(ctx, s.db, `SELECT id,name,repository_url,private,default_branch,provider,status,branches_json,last_synced_at,created_at,updated_at FROM props ORDER BY id`, scanProp)
}

// CreateProp inserts a source repository Prop.
func (s *DB) CreateProp(ctx context.Context, p domain.Prop) (domain.Prop, error) {
	now := time.Now().UTC()
	p = prepareCreatedProp(p, now)
	branches, err := encodePropBranches(&p)
	if err != nil {
		return p, err
	}
	id, err := s.insertProp(ctx, p, branches, now)
	if err != nil {
		return p, err
	}
	p.ID = id
	return p, nil
}

// prepareCreatedProp applies create-time defaults to a Prop.
func prepareCreatedProp(p domain.Prop, now time.Time) domain.Prop {
	p.CreatedAt = now
	p.UpdatedAt = now
	p.Name = strings.TrimSpace(p.Name)
	p.RepositoryURL = strings.TrimSpace(p.RepositoryURL)
	p.DefaultBranch = strings.TrimSpace(p.DefaultBranch)
	p.Provider = strings.ToLower(strings.TrimSpace(p.Provider))
	if p.Name == "" {
		p.Name = inferPropName(p.RepositoryURL)
	}
	if p.DefaultBranch == "" {
		p.DefaultBranch = "main"
	}
	if p.Provider == "" {
		p.Provider = inferProvider(p.RepositoryURL)
	}
	if p.Status == "" {
		p.Status = "active"
	}
	if len(p.Branches) == 0 {
		p.Branches = []string{p.DefaultBranch}
	}
	return p
}

// insertProp writes one prepared Prop row and returns its ID.
func (s *DB) insertProp(ctx context.Context, p domain.Prop, branches string, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO props (name,repository_url,private,default_branch,provider,status,branches_json,last_synced_at,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.RepositoryURL, boolToInt(p.Private), p.DefaultBranch, p.Provider, p.Status, branches, nullableTime(p.LastSyncedAt), encodeTime(now), encodeTime(now))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetProp fetches a Prop by ID or name.
func (s *DB) GetProp(ctx context.Context, identifier string) (domain.Prop, error) {
	where, arg := identifierWhere(identifier)
	return queryOne(ctx, s.db, `SELECT id,name,repository_url,private,default_branch,provider,status,branches_json,last_synced_at,created_at,updated_at FROM props WHERE `+where, arg, scanProp)
}

// FindPropByRepositoryURL fetches the first Prop matching a repository identity.
func (s *DB) FindPropByRepositoryURL(ctx context.Context, repositoryURL string) (domain.Prop, bool, error) {
	props, err := s.ListProps(ctx)
	if err != nil {
		return domain.Prop{}, false, err
	}
	for _, prop := range props {
		if git.SameRepositoryURL(prop.RepositoryURL, repositoryURL) {
			return prop, true, nil
		}
	}
	return domain.Prop{}, false, nil
}

// SaveProp updates an existing Prop row.
func (s *DB) SaveProp(ctx context.Context, p domain.Prop) (domain.Prop, error) {
	p.UpdatedAt = time.Now().UTC()
	p.Name = strings.TrimSpace(p.Name)
	p.RepositoryURL = strings.TrimSpace(p.RepositoryURL)
	p.DefaultBranch = strings.TrimSpace(p.DefaultBranch)
	p.Provider = strings.ToLower(strings.TrimSpace(p.Provider))
	branches, err := encodePropBranches(&p)
	if err != nil {
		return p, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE props SET name=?,repository_url=?,private=?,default_branch=?,provider=?,status=?,branches_json=?,last_synced_at=?,updated_at=? WHERE id=?`,
		p.Name, p.RepositoryURL, boolToInt(p.Private), p.DefaultBranch, p.Provider, p.Status, branches, nullableTime(p.LastSyncedAt), encodeTime(p.UpdatedAt), p.ID)
	if err := requireRowsAffected(res, err); err != nil {
		return p, err
	}
	return p, nil
}

// DeleteProp deletes a Prop by ID or name.
func (s *DB) DeleteProp(ctx context.Context, identifier string) error {
	return s.deleteByIdentifier(ctx, identifier, `DELETE FROM props WHERE id=?`, `DELETE FROM props WHERE name=?`)
}

// PlayspecsReferencingProp returns Playspec names that reference a Prop.
func (s *DB) PlayspecsReferencingProp(ctx context.Context, prop domain.Prop) (names []string, err error) {
	candidates, err := queryRows(ctx, s.db, `SELECT name,base_compose_yaml,services_json FROM playspecs ORDER BY id`, scanPlayspecReferenceCandidate)
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		references, err := candidate.referencesProp(prop)
		if err != nil {
			return nil, err
		}
		if references {
			names = append(names, candidate.name)
		}
	}
	return names, nil
}

// playspecReferenceCandidate is the subset needed to test Prop delete safety.
type playspecReferenceCandidate struct {
	name         string
	composeYAML  string
	servicesJSON string
}

// scanPlayspecReferenceCandidate decodes one Playspec Prop-reference candidate.
func scanPlayspecReferenceCandidate(row scanner) (playspecReferenceCandidate, error) {
	var candidate playspecReferenceCandidate
	err := row.Scan(&candidate.name, &candidate.composeYAML, &candidate.servicesJSON)
	return candidate, err
}

// referencesProp checks whether a candidate references a Prop.
func (c playspecReferenceCandidate) referencesProp(prop domain.Prop) (bool, error) {
	return playspecReferencesProp(c.composeYAML, c.servicesJSON, prop)
}

// playspecReferencesProp checks both Compose labels and services metadata.
func playspecReferencesProp(composeYAML string, servicesJSON string, prop domain.Prop) (bool, error) {
	if references, err := composeReferencesRepositoryURL(composeYAML, prop.RepositoryURL); err != nil || references {
		return references, err
	}
	return servicesReferenceProp(servicesJSON, prop)
}

// servicesReferenceProp checks persisted services metadata for Prop references.
func servicesReferenceProp(servicesJSON string, prop domain.Prop) (bool, error) {
	var services []map[string]any
	if err := decodeStoredJSON(servicesJSON, "playspecs.services_json", &services); err != nil {
		return false, err
	}
	for _, service := range services {
		if serviceReferencesProp(service, prop) {
			return true, nil
		}
	}
	return false, nil
}

// serviceReferencesProp reports whether one services[] item references a Prop.
func serviceReferencesProp(service map[string]any, prop domain.Prop) bool {
	if serviceReferencesPropID(service["prop_id"], prop.ID) {
		return true
	}
	repo, _ := service["repo_url"].(string)
	return repo != "" && git.SameRepositoryURL(repo, prop.RepositoryURL)
}

// composeReferencesRepositoryURL parses Compose labels for canonical repo matches.
func composeReferencesRepositoryURL(composeYAML string, repositoryURL string) (bool, error) {
	repositoryURL = strings.TrimSpace(repositoryURL)
	if repositoryURL == "" || strings.TrimSpace(composeYAML) == "" {
		return false, nil
	}
	services, hasServices, err := composeReferenceServices(composeYAML)
	if err != nil || !hasServices {
		return false, err
	}
	return composeServicesReferenceRepository(services, repositoryURL)
}

// composeServicesReferenceRepository scans Compose services for repo labels.
func composeServicesReferenceRepository(services map[string]any, repositoryURL string) (bool, error) {
	for name, rawService := range services {
		references, err := composeServiceReferencesRepository(name, rawService, repositoryURL)
		if err != nil || references {
			return references, err
		}
	}
	return false, nil
}

// composeReferenceServices parses the raw services map used for Prop locks.
func composeReferenceServices(composeYAML string) (map[string]any, bool, error) {
	var root map[string]any
	if err := yaml.Unmarshal([]byte(composeYAML), &root); err != nil {
		return nil, false, fmt.Errorf("parse playspec compose for prop references: %w", err)
	}
	if root == nil {
		return nil, false, errors.New("parse playspec compose for prop references: expected mapping")
	}
	rawServices, hasServices := root["services"]
	if !hasServices {
		return nil, false, nil
	}
	services, ok := compose.AsMap(rawServices)
	if !ok {
		return nil, false, errors.New("parse playspec compose for prop references: services must be a mapping")
	}
	return services, true, nil
}

// composeServiceReferencesRepository checks one raw Compose service entry.
func composeServiceReferencesRepository(name string, rawService any, repositoryURL string) (bool, error) {
	if strings.TrimSpace(name) == "" {
		return false, errors.New("parse playspec compose for prop references: service name is required")
	}
	service, ok := compose.AsMap(rawService)
	if !ok {
		return false, fmt.Errorf("parse playspec compose for prop references: service %q must be a mapping", name)
	}
	labels := servicepkg.NormalizeLabels(service["labels"])
	return git.SameRepositoryURL(labels["fibe.gg/repo_url"], repositoryURL), nil
}
