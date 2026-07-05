package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// normalizePropBranchRecords merges names and records with default-branch first.
func normalizePropBranchRecords(defaultBranch string, names []string, records []domain.PropBranch) []domain.PropBranch {
	byName := propBranchRecordMap(defaultBranch, names, records)
	orderedNames := orderedPropBranchNames(defaultBranch, byName)
	out := make([]domain.PropBranch, 0, len(orderedNames))
	for _, name := range orderedNames {
		record := byName[name]
		record.Name = name
		record.Default = name == defaultBranch
		out = append(out, record)
	}
	return out
}

// propBranchRecordMap builds canonical branch records by branch name.
func propBranchRecordMap(defaultBranch string, names []string, records []domain.PropBranch) map[string]domain.PropBranch {
	byName := make(map[string]domain.PropBranch, len(records)+len(names)+1)
	for _, record := range records {
		record.Name = strings.TrimSpace(record.Name)
		if record.Name == "" {
			continue
		}
		byName[record.Name] = record
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		record := byName[name]
		record.Name = name
		byName[name] = record
	}
	if defaultBranch != "" {
		record := byName[defaultBranch]
		record.Name = defaultBranch
		byName[defaultBranch] = record
	}
	return byName
}

// orderedPropBranchNames sorts branch names and moves the default branch first.
func orderedPropBranchNames(defaultBranch string, byName map[string]domain.PropBranch) []string {
	orderedNames := make([]string, 0, len(byName))
	for name := range byName {
		orderedNames = append(orderedNames, name)
	}
	sort.Strings(orderedNames)
	return moveBranchFirst(orderedNames, defaultBranch)
}

// moveBranchFirst moves one branch name to the front without reordering others.
func moveBranchFirst(names []string, branch string) []string {
	if branch == "" {
		return names
	}
	for i, name := range names {
		if name != branch {
			continue
		}
		return append([]string{name}, append(names[:i], names[i+1:]...)...)
	}
	return names
}

// scanProp decodes one Prop row and its branch metadata.
func scanProp(row scanner) (domain.Prop, error) {
	var p domain.Prop
	var private int
	var branchesJSON string
	var last sql.NullString
	var created, updated string
	err := row.Scan(&p.ID, &p.Name, &p.RepositoryURL, &private, &p.DefaultBranch, &p.Provider, &p.Status, &branchesJSON, &last, &created, &updated)
	if err != nil {
		return p, err
	}
	p.Private = private == 1
	branches, err := decodePropBranches(branchesJSON, p.DefaultBranch)
	if err != nil {
		return p, err
	}
	p.Branches = branches.names
	p.BranchRecords = branches.records
	if err := assignNullableStoredTime("props.last_synced_at", last, &p.LastSyncedAt); err != nil {
		return p, err
	}
	if p.CreatedAt, err = parseStoredTime("props.created_at", created); err != nil {
		return p, err
	}
	if p.UpdatedAt, err = parseStoredTime("props.updated_at", updated); err != nil {
		return p, err
	}
	return p, nil
}

// encodePropBranches normalizes Prop branch records before persistence.
func encodePropBranches(p *domain.Prop) (string, error) {
	p.BranchRecords = normalizePropBranchRecords(p.DefaultBranch, p.Branches, p.BranchRecords)
	p.Branches = propBranchNames(p.BranchRecords)
	return encodeStoredJSON("props.branches_json", p.BranchRecords, "[]")
}

// decodedPropBranches carries branch names and their record metadata.
type decodedPropBranches struct {
	names   []string
	records []domain.PropBranch
}

// decodePropBranches supports both current branch records and legacy name arrays.
func decodePropBranches(raw string, defaultBranch string) (decodedPropBranches, error) {
	if storedJSONIsNull(raw) {
		return decodedPropBranches{}, errors.New("decode props.branches_json: stored JSON null is not canonical")
	}
	var records []domain.PropBranch
	if err := json.Unmarshal([]byte(raw), &records); err == nil {
		records = normalizePropBranchRecords(defaultBranch, nil, records)
		return decodedPropBranches{names: propBranchNames(records), records: records}, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return decodedPropBranches{}, fmt.Errorf("decode props.branches_json: %w", err)
	}
	records = normalizePropBranchRecords(defaultBranch, names, nil)
	return decodedPropBranches{names: propBranchNames(records), records: records}, nil
}

// propBranchNames extracts non-empty branch names from branch records.
func propBranchNames(records []domain.PropBranch) []string {
	names := make([]string, 0, len(records))
	for _, record := range records {
		if record.Name != "" {
			names = append(names, record.Name)
		}
	}
	return names
}
