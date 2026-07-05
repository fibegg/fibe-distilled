package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
)

// Traefik runtime constants keep Docker-provider behavior deterministic.
const (
	traefikDockerAPIVersion   = "1.40"
	traefikImage              = "traefik:v3.7"
	fibeDistilledManagedLabel = "fibe-distilled.managed"
)

// ensureTraefik starts or refreshes the per-Marquee Traefik runtime.
func (c Checker) ensureTraefik(ctx context.Context, fsys RemoteFS, docker DockerRuntime, marquee domain.Marquee) error {
	rootDomain := domain.FirstDomainFromInput(marquee.DomainsInput)
	if rootDomain == "" {
		return nil
	}
	if err := c.prepareTraefikRuntime(ctx, fsys, marquee); err != nil {
		return err
	}
	args, err := c.traefikArgs(marquee)
	if err != nil {
		return err
	}
	if err := docker.EnsureTraefik(ctx, marquee, args); err != nil {
		return fmt.Errorf("start traefik runtime failed: %w", err)
	}
	return nil
}

// prepareTraefikRuntime creates Traefik state and Docker config files.
func (c Checker) prepareTraefikRuntime(ctx context.Context, fsys RemoteFS, marquee domain.Marquee) error {
	if err := fsys.MkdirAll(ctx, marquee, optfibe.TraefikDockerConfigDir(), 0o700); err != nil {
		return fmt.Errorf("prepare traefik runtime failed: %w", err)
	}
	acmePath := optfibe.TraefikACMEPath()
	if _, err := fsys.ReadRemoteFile(ctx, marquee, acmePath); errors.Is(err, ErrRemoteFileMissing) {
		if err := fsys.WriteRemoteFile(ctx, marquee, acmePath, []byte{}, 0o600); err != nil {
			return fmt.Errorf("prepare traefik acme storage failed: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("prepare traefik acme storage failed: %w", err)
	} else if err := fsys.Chmod(ctx, marquee, acmePath, 0o600); err != nil {
		return fmt.Errorf("prepare traefik acme permissions failed: %w", err)
	}
	return c.writeDockerConfigDir(ctx, fsys, marquee, optfibe.TraefikDockerConfigDir())
}

// traefikArgs returns the static Traefik CLI arguments for a Marquee.
func (c Checker) traefikArgs(marquee domain.Marquee) ([]string, error) {
	email := ""
	if marquee.AcmeEmail != nil {
		email = strings.TrimSpace(*marquee.AcmeEmail)
	}
	if email == "" {
		return nil, fmt.Errorf("marquee %q requires acme_email for HTTPS routing", marquee.Name)
	}
	return []string{
		"--api=false",
		"--providers.docker=true",
		"--providers.docker.exposedbydefault=false",
		"--providers.docker.constraints=Label(`" + fibeDistilledManagedLabel + "`,`true`)",
		"--entrypoints.web.address=:80",
		"--entrypoints.websecure.address=:443",
		"--entrypoints.web.http.redirections.entrypoint.to=websecure",
		"--entrypoints.web.http.redirections.entrypoint.scheme=https",
		"--certificatesresolvers.letsencrypt.acme.email=" + email,
		"--certificatesresolvers.letsencrypt.acme.storage=/etc/traefik/acme.json",
		"--certificatesresolvers.letsencrypt.acme.httpchallenge=true",
		"--certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=web",
	}, nil
}
