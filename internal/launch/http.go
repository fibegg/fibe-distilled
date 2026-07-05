package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/api/request"
	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/git"
	playgroundpkg "github.com/fibegg/fibe-distilled/internal/playground"
	playspecpkg "github.com/fibegg/fibe-distilled/internal/playspec"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// maxLocalComposeFileBytes caps loopback CLI compose-path reads.
const maxLocalComposeFileBytes int64 = 16 << 20

// launchPlan is the normalized launch request used after validation.
type launchPlan struct {
	Name             string
	ComposeYAML      string
	RepositoryURL    string
	MarqueeID        *int64
	CreatePlayground bool
	PersistVolumes   *bool
	EnvOverrides     map[string]string
	Services         map[string]any
}

// launchResources tracks resources created during a launch for cleanup.
type launchResources struct {
	Playspec        domain.Playspec
	PlayspecCreated bool
	PropIDs         []int64
	PlaygroundID    int64
}

// decodeLaunchPayload decodes the launch request envelope-free body.
func decodeLaunchPayload(w http.ResponseWriter, r *http.Request) (launchPayload, bool) {
	var body launchPayload
	return body, request.Decode(w, r, &body)
}

// prepareLaunchPlan validates launch input and renders launch-time overrides.
func (h Handler) prepareLaunchPlan(w http.ResponseWriter, r *http.Request, body launchPayload) (launchPlan, bool) {
	if err := validateLaunchRequest(body); err != nil {
		writePayloadErr(w, r, err)
		return launchPlan{}, false
	}
	if err := validateLaunchRepository(body.RepositoryURL); err != nil {
		response.BadRequest(w, r, err.Error())
		return launchPlan{}, false
	}

	plan := newLaunchPlan(body)
	if err := resolveLaunchComposePath(r, &plan); err != nil {
		response.BadRequest(w, r, err.Error())
		return launchPlan{}, false
	}
	rootDomain, ok := h.resolveLaunchRootDomain(w, r, &body, &plan)
	if !ok {
		return launchPlan{}, false
	}
	patched, err := renderLaunchCompose(body, plan, rootDomain)
	if err != nil {
		response.BadRequest(w, r, err.Error())
		return launchPlan{}, false
	}
	plan.ComposeYAML = patched
	if err := h.requireLaunchRepositoriesWritable(r.Context(), plan); err != nil {
		writeRuntimeWritableErr(w, r, err)
		return launchPlan{}, false
	}
	return plan, true
}

// resolveLaunchRootDomain resolves Marquee references and returns its root domain.
func (h Handler) resolveLaunchRootDomain(w http.ResponseWriter, r *http.Request, body *launchPayload, plan *launchPlan) (string, bool) {
	if err := h.resolveLaunchReferences(r.Context(), body); err != nil {
		writeStoreErr(w, r, "marquee", err)
		return "", false
	}
	plan.MarqueeID = body.MarqueeID
	rootDomain, err := h.launchRootDomain(r.Context(), plan.MarqueeID)
	if errors.Is(err, store.ErrNotFound) {
		response.NotFound(w, r, "marquee")
		return "", false
	}
	if err != nil {
		response.ServerError(w, r, err)
		return "", false
	}
	return rootDomain, true
}

// renderLaunchCompose applies launch variables and service overrides.
func renderLaunchCompose(body launchPayload, plan launchPlan, rootDomain string) (string, error) {
	return ApplyOverrides(
		plan.ComposeYAML,
		body.Variables,
		body.ServiceSubdomains,
		body.Services,
		OverrideOptions{RootDomain: rootDomain},
	)
}

// newLaunchPlan derives defaults from the raw payload.
func newLaunchPlan(body launchPayload) launchPlan {
	name := normalizeScalarInput(body.Name)
	if name == "" {
		name = nameFromRepo(body.RepositoryURL)
	}
	createPG := body.CreatePlayground == nil || *body.CreatePlayground
	return launchPlan{
		Name:             name,
		ComposeYAML:      body.ComposeYAML,
		RepositoryURL:    normalizeRepositoryURLInput(body.RepositoryURL),
		MarqueeID:        body.MarqueeID,
		CreatePlayground: createPG,
		PersistVolumes:   body.PersistVolumes,
		EnvOverrides:     body.EnvOverrides,
		Services:         body.Services,
	}
}

// resolveLaunchComposePath reads local Compose files for loopback CLI calls only.
func resolveLaunchComposePath(r *http.Request, plan *launchPlan) error {
	value := strings.TrimSpace(plan.ComposeYAML)
	if !looksLikeComposePath(value) {
		return nil
	}
	if !isLoopbackRequest(r) {
		return errors.New("compose_yaml local paths are only supported from loopback requests; pass compose YAML content instead")
	}
	data, err := readLocalComposeFile(value)
	if err != nil {
		return fmt.Errorf("read compose_yaml path %q: %w", value, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return fmt.Errorf("compose_yaml path %q is empty", value)
	}
	plan.ComposeYAML = string(data)
	return nil
}

// readLocalComposeFile reads a regular Compose file with a hard size cap.
func readLocalComposeFile(path string) ([]byte, error) {
	// #nosec G304,G703 -- loopback-only CLI compatibility reads an authenticated local Compose file path; remote API callers must send YAML content.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, statErr := file.Stat()
	if statErr != nil {
		closeErr := file.Close()
		return nil, errors.Join(statErr, closeErr)
	}
	if !info.Mode().IsRegular() {
		closeErr := file.Close()
		return nil, errors.Join(errors.New("not a regular file"), closeErr)
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, maxLocalComposeFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(raw)) > maxLocalComposeFileBytes {
		return nil, fmt.Errorf("file exceeds %d byte limit", maxLocalComposeFileBytes)
	}
	return raw, nil
}

// looksLikeComposePath detects plain local .yml/.yaml values.
func looksLikeComposePath(value string) bool {
	if strings.ContainsAny(value, "\n\r") {
		return false
	}
	switch strings.ToLower(filepath.Ext(value)) {
	case ".yml", ".yaml":
		return true
	default:
		return false
	}
}

// isLoopbackRequest reports whether the HTTP client is local to fibe-distilled.
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// validateLaunchRequest validates launch payload and runtime-only fields.
func validateLaunchRequest(body launchPayload) error {
	if err := validateLaunchPayload(body); err != nil {
		return err
	}
	return validateLaunchRuntimeFields(body)
}

// validateLaunchRepository rejects embedded repository credentials.
func validateLaunchRepository(repositoryURL string) error {
	if repositoryURL == "" {
		return nil
	}
	return validateRuntimeRepositoryURL(repositoryURL)
}

// requireLaunchRepositoriesWritable checks all GitHub repos used by a launch.
func (h Handler) requireLaunchRepositoriesWritable(ctx context.Context, plan launchPlan) error {
	if err := h.requireRuntimeWritable(ctx, GitHubRepositoryURLs(plan.ComposeYAML)); err != nil {
		return err
	}
	if plan.RepositoryURL == "" {
		return nil
	}
	return h.requireRuntimeWritable(ctx, []string{plan.RepositoryURL})
}

// launchRootDomain returns the configured Marquee root domain for templating.
func (h Handler) launchRootDomain(ctx context.Context, marqueeID *int64) (string, error) {
	if marqueeID == nil {
		return "", nil
	}
	marquee, err := h.repo.GetMarquee(ctx, idString(*marqueeID))
	if err != nil {
		return "", err
	}
	return domain.FirstDomainFromInput(marquee.DomainsInput), nil
}

// createLaunchResources creates or reuses launch Playspec and Prop records.
func (h Handler) createLaunchResources(w http.ResponseWriter, r *http.Request, plan launchPlan) (launchResources, bool) {
	playspecResult := h.createLaunchPlayspec(w, r, plan)
	if !playspecResult.ok {
		return launchResources{}, false
	}
	resources := launchResources{Playspec: playspecResult.playspec, PlayspecCreated: playspecResult.created}
	if plan.RepositoryURL == "" {
		return resources, true
	}
	return h.createLaunchPropResource(w, r, plan, resources)
}

// createLaunchPropResource creates or reuses the launch Prop record.
func (h Handler) createLaunchPropResource(w http.ResponseWriter, r *http.Request, plan launchPlan, resources launchResources) (launchResources, bool) {
	prop, created, err := h.ensureLaunchProp(r.Context(), plan.Name, plan.RepositoryURL)
	if err != nil {
		h.writeLaunchPropCreateErr(w, r, plan, resources, err)
		return launchResources{}, false
	}
	if created {
		resources.PropIDs = append(resources.PropIDs, prop.ID)
	}
	return resources, true
}

// writeLaunchPropCreateErr maps Prop creation failures during launch.
func (h Handler) writeLaunchPropCreateErr(w http.ResponseWriter, r *http.Request, plan launchPlan, resources launchResources, err error) {
	if cleanupErr := h.cleanupLaunchResources(r.Context(), resources.createdPlayspecID(), nil); cleanupErr != nil {
		response.ServerError(w, r, errors.Join(err, fmt.Errorf("cleanup launch resources: %w", cleanupErr)))
		return
	}
	if conflict, ok := errors.AsType[conflictError](err); ok {
		response.Error(w, r, http.StatusConflict, "RESOURCE_IN_USE", conflict.message, map[string]any{"name": plan.Name})
		return
	}
	response.ServerError(w, r, err)
}

// launchPlayspecResult carries a launch Playspec create or replay outcome.
type launchPlayspecResult struct {
	playspec domain.Playspec
	created  bool
	ok       bool
}

// createLaunchPlayspec creates or replays the named launch Playspec.
func (h Handler) createLaunchPlayspec(w http.ResponseWriter, r *http.Request, plan launchPlan) launchPlayspecResult {
	playspec, err := playspecpkg.NewResource(playspecpkg.ResourceInput{
		Name:            plan.Name,
		BaseComposeYAML: plan.ComposeYAML,
		PersistVolumes:  plan.PersistVolumes,
	})
	if err != nil {
		writePayloadErr(w, r, err)
		return launchPlayspecResult{}
	}
	created, err := h.repo.CreatePlayspec(r.Context(), playspec)
	if err == nil {
		return launchPlayspecResult{playspec: created, created: true, ok: true}
	}
	if !store.IsUniqueConstraint(err) {
		response.ServerError(w, r, err)
		return launchPlayspecResult{}
	}
	existing, getErr := h.repo.GetPlayspec(r.Context(), plan.Name)
	if getErr != nil {
		response.ServerError(w, r, errors.Join(err, getErr))
		return launchPlayspecResult{}
	}
	if !sameLaunchPlayspec(existing, playspec) {
		response.Error(w, r, http.StatusConflict, "RESOURCE_IN_USE", "launch playspec name already exists for a different compose", map[string]any{"name": plan.Name})
		return launchPlayspecResult{}
	}
	return launchPlayspecResult{playspec: existing, ok: true}
}

// createLaunchPlayground creates the runtime Playground for a launch.
func (h Handler) createLaunchPlayground(w http.ResponseWriter, r *http.Request, plan launchPlan, resources *launchResources) bool {
	playground, err := h.deployLaunchPlayground(r.Context(), plan, resources.Playspec)
	if err != nil {
		if playground.ID == 0 {
			if cleanupErr := h.cleanupLaunchResources(r.Context(), resources.createdPlayspecID(), resources.PropIDs); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("cleanup launch resources: %w", cleanupErr))
			}
		}
		writeCreatePlaygroundErr(w, r, err)
		return false
	}
	resources.PlaygroundID = playground.ID
	return true
}

// deployLaunchPlayground creates and deploys a Playground from launch resources.
func (h Handler) deployLaunchPlayground(ctx context.Context, plan launchPlan, playspec domain.Playspec) (domain.Playground, error) {
	payload := playgroundpkg.CreatePayload{
		Name:         plan.Name,
		PlayspecID:   playspec.ID,
		MarqueeID:    plan.MarqueeID,
		EnvOverrides: plan.EnvOverrides,
		Services:     plan.Services,
	}
	if plan.RepositoryURL != "" {
		return h.deployRuntimePlayground(ctx, payload, true)
	}
	return h.deployRuntimePlayground(ctx, payload, false)
}

// writeLaunchCreated writes the SDK-compatible launch success payload.
func writeLaunchCreated(w http.ResponseWriter, r *http.Request, plan launchPlan, resources launchResources) {
	propsCreated := resources.PropIDs
	if propsCreated == nil {
		propsCreated = []int64{}
	}
	payload := map[string]any{
		"playspec_id":   *resources.Playspec.ID,
		"playground_id": nil,
		"props_created": propsCreated,
	}
	if resources.PlaygroundID != 0 {
		payload["playground_id"] = resources.PlaygroundID
	}
	if plan.RepositoryURL != "" {
		payload["source"] = map[string]any{"repository_url": plan.RepositoryURL}
	}
	response.JSON(w, r, http.StatusCreated, payload)
}

// resolveLaunchReferences resolves Marquee name-or-ID references.
func (h Handler) resolveLaunchReferences(ctx context.Context, payload *launchPayload) error {
	id, err := h.resolveConfiguredMarqueeID(ctx, payload.MarqueeID, payload.marqueeIdentifier)
	if err != nil {
		return err
	}
	payload.MarqueeID = id
	return nil
}

// ensureLaunchProp creates or reuses the launch Prop for a repository.
func (h Handler) ensureLaunchProp(ctx context.Context, name string, repositoryURL string) (domain.Prop, bool, error) {
	prop, err := h.repo.CreateProp(ctx, domain.Prop{RepositoryURL: repositoryURL, Name: name})
	if err == nil {
		return prop, true, nil
	}
	if !store.IsUniqueConstraint(err) {
		return domain.Prop{}, false, err
	}
	existing, getErr := h.repo.GetProp(ctx, name)
	if getErr != nil {
		return domain.Prop{}, false, errors.Join(err, getErr)
	}
	if !git.SameRepositoryURL(existing.RepositoryURL, repositoryURL) {
		return domain.Prop{}, false, conflictError{message: "launch prop name already exists for a different repository"}
	}
	return existing, false, nil
}

// createdPlayspecID returns a cleanup ID only for newly created Playspecs.
func (r launchResources) createdPlayspecID() *int64 {
	if !r.PlayspecCreated {
		return nil
	}
	return r.Playspec.ID
}

// sameLaunchPlayspec allows replay only for equivalent stateless Playspecs.
func sameLaunchPlayspec(existing domain.Playspec, requested domain.Playspec) bool {
	existingStateless := existing.PersistVolumes == nil || !*existing.PersistVolumes
	requestedStateless := requested.PersistVolumes == nil || !*requested.PersistVolumes
	return strings.TrimSpace(existing.BaseComposeYAML) == strings.TrimSpace(requested.BaseComposeYAML) &&
		existingStateless &&
		requestedStateless
}

// cleanupLaunchResources removes records created by a failed launch.
func (h Handler) cleanupLaunchResources(ctx context.Context, playspecID *int64, propIDs []int64) error {
	if err := h.cleanupLaunchPlayspec(ctx, playspecID); err != nil {
		return err
	}
	return h.cleanupLaunchProps(ctx, propIDs)
}

// cleanupLaunchPlayspec removes a newly created launch Playspec.
func (h Handler) cleanupLaunchPlayspec(ctx context.Context, playspecID *int64) error {
	if playspecID != nil {
		return ignoreNotFound(h.repo.DeletePlayspec(ctx, idString(*playspecID)))
	}
	return nil
}

// cleanupLaunchProps removes newly created launch Props.
func (h Handler) cleanupLaunchProps(ctx context.Context, propIDs []int64) error {
	for _, propID := range propIDs {
		if propID == 0 {
			continue
		}
		if err := ignoreNotFound(h.repo.DeleteProp(ctx, idString(propID))); err != nil {
			return err
		}
	}
	return nil
}
