package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	compose "github.com/fibegg/fibe-distilled/internal/composefile"
	fibetemplate "github.com/fibegg/fibe-distilled/internal/composefile/template"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/playground"
)

// renderedPlayground bundles render output used by later deployment phases.
type renderedPlayground struct {
	pg          domain.Playground
	playspec    domain.Playspec
	project     string
	composeYAML string
	services    []domain.PlaygroundServiceInfo
}

// renderPlayground applies overrides and produces runtime Compose plus URLs.
func (w Worker) renderPlayground(ctx context.Context, pg domain.Playground, ps domain.Playspec, mq domain.Marquee) (renderedPlayground, error) {
	routing := configurePlaygroundRouting(pg, mq)
	pg = routing.playground
	project := playgroundProject(pg)
	render := renderedPlayground{pg: pg, project: project}
	inputs, err := preparedRenderInputs(ps, pg)
	if err != nil {
		render.pg = inputs.playground
		return w.failComposeRender(ctx, render, err, nil)
	}
	pg = inputs.playground
	rendered, err := runtimeComposeForPlayground(inputs.playspec, pg, project, routing.rootDomain, routing.scheme)
	if err != nil {
		render.pg = pg
		return w.failComposeRender(ctx, render, err, nil)
	}
	// A marquee with no domains_input cannot route exposed services: no Traefik
	// router is generated and the service URLs are malformed. Fail clearly instead
	// of reporting a "running" playground with unreachable URLs.
	if routing.rootDomain == "" && len(rendered.ServiceURLs) > 0 {
		domainErr := fmt.Errorf("marquee %q has no domains_input; exposed services cannot be routed (set domains on the marquee)", mq.Name)
		render.pg = pg
		return w.failComposeRender(ctx, render, domainErr, rendered.Services)
	}
	pg.GeneratedComposeYAML = rendered.ComposeYAML
	pg.Services = rendered.Services
	pg.ServiceURLs = rendered.ServiceURLs
	pg, err = w.recordCreationStep(ctx, pg, "compose_render", "completed", nil)
	if err != nil {
		render.pg = pg
		return render, err
	}
	return renderedPlayground{pg: pg, playspec: inputs.playspec, project: project, composeYAML: rendered.ComposeYAML, services: rendered.Services}, nil
}

// preparedRenderInput carries render-time Playground and effective Playspec data.
type preparedRenderInput struct {
	playground domain.Playground
	playspec   domain.Playspec
}

// preparedRenderInputs ensures secrets and applies Playground overrides.
func preparedRenderInputs(ps domain.Playspec, pg domain.Playground) (preparedRenderInput, error) {
	pg, err := ensureInternalPassword(pg)
	if err != nil {
		return preparedRenderInput{playground: pg, playspec: ps}, err
	}
	effectivePS, err := effectivePlaygroundPlayspec(ps, pg)
	return preparedRenderInput{playground: pg, playspec: effectivePS}, err
}

// runtimeComposeForPlayground renders Compose and service metadata.
func runtimeComposeForPlayground(ps domain.Playspec, pg domain.Playground, project string, rootDomain string, scheme string) (compose.RuntimeResult, error) {
	return compose.RuntimeWithOptions(ps.BaseComposeYAML, project, rootDomain, scheme, compose.RuntimeOptions{
		InternalPassword: *pg.InternalPassword,
		PlaygroundID:     pg.ID,
		ServicePasswords: serviceAuthPasswords(pg.ServiceBranches),
	})
}

// failComposeRender records render failure in creation progress and Playground state.
func (w Worker) failComposeRender(ctx context.Context, render renderedPlayground, err error, services []domain.PlaygroundServiceInfo) (renderedPlayground, error) {
	var progressErr error
	render.pg, progressErr = w.recordCreationStep(ctx, render.pg, "compose_render", "error", err)
	failed, failErr := w.failDeployment(ctx, render.pg, err, playgroundServiceNames(services))
	render.pg = failed
	return render, errors.Join(failErr, progressErr)
}

// playgroundRouting carries the Playground with routing fields and their values.
type playgroundRouting struct {
	playground domain.Playground
	rootDomain string
	scheme     string
}

// configurePlaygroundRouting derives root domain and scheme from a Marquee.
func configurePlaygroundRouting(pg domain.Playground, mq domain.Marquee) playgroundRouting {
	rootDomain := domain.FirstDomainFromInput(mq.DomainsInput)
	scheme := "https"
	pg.RootDomain = &rootDomain
	pg.RoutingScheme = &scheme
	return playgroundRouting{playground: pg, rootDomain: rootDomain, scheme: scheme}
}

// playgroundProject returns the persisted Compose project name.
func playgroundProject(pg domain.Playground) string {
	if pg.ComposeProject == nil {
		return ""
	}
	return *pg.ComposeProject
}

// ensureInternalPassword creates the per-Playground internal auth secret.
func ensureInternalPassword(pg domain.Playground) (domain.Playground, error) {
	if pg.InternalPassword != nil && *pg.InternalPassword != "" {
		return pg, nil
	}
	password, err := newInternalPassword()
	if err != nil {
		return pg, err
	}
	pg.InternalPassword = &password
	return pg, nil
}

// effectivePlaygroundPlayspec applies Playground overrides to a Playspec.
func effectivePlaygroundPlayspec(ps domain.Playspec, pg domain.Playground) (domain.Playspec, error) {
	patched, err := playground.ApplyOverrides(ps.BaseComposeYAML, pg.EnvOverrides, pg.ServiceBranches)
	if err != nil {
		return ps, err
	}
	ps.BaseComposeYAML = patched
	if fibetemplate.HasUnresolvedTokens(ps.BaseComposeYAML) {
		return ps, errors.New("compose contains unresolved Fibe template variables; launch with variables before deploying a Playground")
	}
	if validation := compose.Validate(ps.BaseComposeYAML); !validation.Valid {
		return ps, errors.New(strings.Join(validation.Errors, "; "))
	}
	return ps, nil
}
