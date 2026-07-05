package prop

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	apilist "github.com/fibegg/fibe-distilled/internal/api/list"
	"github.com/fibegg/fibe-distilled/internal/api/request"
	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/git"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// jsonFields aliases shared JSON field-presence tracking.
type jsonFields = request.JSONFields

// decodePayloadInto decodes JSON into an alias type and records fields.
func decodePayloadInto[T any](data []byte, dst *T, fields *jsonFields) error {
	return request.DecodePayloadInto(data, dst, fields)
}

// decode decodes one required JSON request body.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	return request.Decode(w, r, dst)
}

// pathValue reads a route path parameter.
func pathValue(r *http.Request, key string) string {
	return request.PathValue(r, key)
}

// idString formats positive persisted IDs for name-or-ID lookups.
func idString(id int64) string {
	return strconv.FormatInt(id, 10)
}

// writeLoadedResource writes a loaded resource response.
func writeLoadedResource[T any](w http.ResponseWriter, r *http.Request, load func(http.ResponseWriter, *http.Request) (T, bool)) {
	item, ok := load(w, r)
	if !ok {
		return
	}
	response.JSON(w, r, http.StatusOK, item)
}

// loadProp loads a Prop using the route identifier.
func (h Handler) loadProp(w http.ResponseWriter, r *http.Request) (domain.Prop, bool) {
	p, err := h.repo.GetProp(r.Context(), pathValue(r, "identifier"))
	if err != nil {
		writeStoreErr(w, r, "prop", err)
		return p, false
	}
	return p, true
}

// writeStoreErr maps storage failures to Prop responses.
func writeStoreErr(w http.ResponseWriter, r *http.Request, resource string, err error) {
	if errors.Is(err, store.ErrNotFound) {
		response.NotFound(w, r, resource)
		return
	}
	response.ServerError(w, r, err)
}

// writePayloadErr maps payload validation errors to API responses.
func writePayloadErr(w http.ResponseWriter, r *http.Request, err error) {
	response.ValidationOrBadRequest(w, r, err)
}

// writeFilteredList loads, filters, and writes a list envelope.
func writeFilteredList[T any](
	w http.ResponseWriter,
	r *http.Request,
	load func(context.Context) ([]T, error),
	filter func(*http.Request, []T) ([]T, error),
) {
	apilist.WriteFiltered(w, r, load, filter)
}

// apiValidationError carries a structured Prop API error response.
type apiValidationError struct {
	status  int
	code    string
	message string
	details map[string]any
}

// Error returns the validation message.
func (e apiValidationError) Error() string {
	return e.message
}

// ResponseStatus returns the HTTP status used for structured validation errors.
func (e apiValidationError) ResponseStatus() int {
	return e.status
}

// ResponseCode returns the client-visible validation code.
func (e apiValidationError) ResponseCode() string {
	return e.code
}

// ResponseDetails returns structured validation details.
func (e apiValidationError) ResponseDetails() map[string]any {
	return e.details
}

// badRequestError carries a Prop bad-request message.
type badRequestError struct {
	message string
}

// Error returns the bad-request message.
func (e badRequestError) Error() string {
	return e.message
}

// BadRequestMessage returns the client-visible BAD_REQUEST message.
func (e badRequestError) BadRequestMessage() string {
	return e.message
}

// presentBlankString reports a present string field that is blank.
func presentBlankString(fields jsonFields, name string, value string) bool {
	return fields.Has(name) && strings.TrimSpace(value) == ""
}

// presentBlankStringPtr reports a present optional string field that is blank.
func presentBlankStringPtr(fields jsonFields, name string, value *string) bool {
	return fields.Has(name) && (value == nil || strings.TrimSpace(*value) == "")
}

// normalizeRepositoryURLInput trims transport noise while preserving URL spelling.
func normalizeRepositoryURLInput(raw string) string {
	return normalizeScalarInput(raw)
}

// normalizeScalarInput trims surrounding whitespace from accepted text fields.
func normalizeScalarInput(raw string) string {
	return strings.TrimSpace(raw)
}

// validateRepositoryURLNoCredentials rejects embedded Git credentials.
func validateRepositoryURLNoCredentials(raw string) error {
	if git.RepositoryURLHasCredentials(raw) {
		return badRequestError{message: "repository_url must not include credentials; use process credentials or SSH access"}
	}
	return nil
}

// validateRuntimeRepositoryURL rejects repository targets that runtime Git cannot clone.
func validateRuntimeRepositoryURL(raw string) error {
	if err := validateRepositoryURLNoCredentials(raw); err != nil {
		return err
	}
	if !git.CloneableRepositoryURL(raw) {
		return badRequestError{message: "repository_url must be a cloneable Git URL or SSH target"}
	}
	return nil
}

// validRepoStatusURL accepts supported repository URL forms for status checks.
func validRepoStatusURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if _, ok := git.RepositoryFullName(raw); ok {
		return true
	}
	return git.CloneableRepositoryURL(raw)
}

// isGitHubURL reports whether a URL points to github.com owner/repo.
func isGitHubURL(raw string) bool {
	_, ok := git.RepositoryFullName(raw)
	return ok
}

// filterProps applies supported Prop list filters.
func filterProps(r *http.Request, items []domain.Prop) ([]domain.Prop, error) {
	q := r.URL.Query()
	filters, err := propFilterParams(q)
	if err != nil {
		return nil, err
	}
	filtered := apilist.SelectItems(items, filters.matches)
	return apilist.ApplyNamedCommon(
		r,
		filtered,
		func(item domain.Prop) string { return item.Name },
		func(item domain.Prop) time.Time { return item.CreatedAt },
	)
}

// propFilters stores parsed Prop list filters.
type propFilters struct {
	query       string
	name        string
	private     bool
	privateSet  bool
	status      string
	statusSet   bool
	provider    string
	providerSet bool
}

// propFilterParams parses Prop list query filters.
func propFilterParams(q url.Values) (propFilters, error) {
	filters := propFilters{query: q.Get("q"), name: q.Get("name")}
	private, err := apilist.QueryBool(q, "private")
	if err != nil {
		return filters, badRequestError{message: err.Error()}
	}
	filters.private = private.Value
	filters.privateSet = private.Present
	status, err := apilist.QueryExact(q, "status")
	if err != nil {
		return filters, badRequestError{message: err.Error()}
	}
	filters.status = status.Value
	filters.statusSet = status.Present
	provider, err := apilist.QueryExact(q, "provider")
	if err != nil {
		return filters, badRequestError{message: err.Error()}
	}
	filters.provider = provider.Value
	filters.providerSet = provider.Present
	if filters.providerSet {
		filters.provider = normalizePropProvider(filters.provider)
	}
	return filters, nil
}

// matches reports whether a Prop satisfies all filters.
func (f propFilters) matches(item domain.Prop) bool {
	return f.matchesPrivate(item) &&
		f.matchesStatus(item) &&
		f.matchesProvider(item) &&
		f.matchesQuery(item) &&
		apilist.MatchesText(item.Name, f.name)
}

// matchesPrivate applies the optional private filter.
func (f propFilters) matchesPrivate(item domain.Prop) bool {
	return !f.privateSet || item.Private == f.private
}

// matchesStatus applies the optional status filter.
func (f propFilters) matchesStatus(item domain.Prop) bool {
	return !f.statusSet || item.Status == f.status
}

// matchesProvider applies the optional provider filter.
func (f propFilters) matchesProvider(item domain.Prop) bool {
	return !f.providerSet || item.Provider == f.provider
}

// matchesQuery applies the optional free-text query filter.
func (f propFilters) matchesQuery(item domain.Prop) bool {
	return apilist.MatchesText(item.Name, f.query) || apilist.MatchesText(item.RepositoryURL, f.query)
}

// normalizeBranchRecords returns legacy names plus full branch metadata.
func normalizeBranchRecords(defaultBranch string, records []domain.PropBranch, syncedAt time.Time) ([]string, []domain.PropBranch) {
	defaultBranch = strings.TrimSpace(defaultBranch)
	byName := branchRecordMap(defaultBranch, records)
	names := sortedBranchRecordNames(byName, defaultBranch)
	branches := make([]string, 0, len(names))
	branchRecords := make([]domain.PropBranch, 0, len(names))
	for _, name := range names {
		record := normalizedBranchRecord(byName[name], name, defaultBranch, syncedAt)
		branches = append(branches, name)
		branchRecords = append(branchRecords, record)
	}
	return branches, branchRecords
}

// branchRecordMap deduplicates records and ensures the default branch exists.
func branchRecordMap(defaultBranch string, records []domain.PropBranch) map[string]domain.PropBranch {
	byName := map[string]domain.PropBranch{}
	for _, record := range records {
		record.Name = strings.TrimSpace(record.Name)
		if record.Name != "" {
			byName[record.Name] = record
		}
	}
	if defaultBranch != "" {
		record := byName[defaultBranch]
		record.Name = defaultBranch
		byName[defaultBranch] = record
	}
	return byName
}

// sortedBranchRecordNames returns deterministic branch order with default first.
func sortedBranchRecordNames(byName map[string]domain.PropBranch, defaultBranch string) []string {
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	return moveNameToFront(names, defaultBranch)
}

// moveNameToFront moves the target name to the front when present.
func moveNameToFront(names []string, target string) []string {
	if target == "" {
		return names
	}
	for i, name := range names {
		if name == target {
			return append([]string{name}, append(names[:i], names[i+1:]...)...)
		}
	}
	return names
}

// normalizedBranchRecord fills stable branch metadata defaults.
func normalizedBranchRecord(record domain.PropBranch, name string, defaultBranch string, syncedAt time.Time) domain.PropBranch {
	record.Name = name
	record.Default = name == defaultBranch
	if record.LastSyncedAt == nil {
		t := syncedAt
		record.LastSyncedAt = &t
	}
	return record
}
