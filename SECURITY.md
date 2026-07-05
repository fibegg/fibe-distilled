# Security policy

## Trust model

fibe-distilled is a **single-tenant, single-operator** server. It is designed to be run by one trusted operator who
deploys their own Docker Compose Playspecs onto the local Docker host mounted into fibe-distilled. Please read
[docs/LIMITATIONS.md](docs/LIMITATIONS.md) for the full model. In particular:

- `FIBE_API_KEY` is a full-access admin credential — protect it like a root password and serve the API over
  a trusted network or behind TLS.
- Playspecs are arbitrary Docker Compose run on your Marquee and are **not** sandboxed. Do not expose fibe-distilled as a
  multi-tenant service that accepts compose from untrusted users.
- Mounting `/var/run/docker.sock` gives fibe-distilled root-equivalent control over the Docker host. The published Docker
  image intentionally runs as root so it can use that socket and write `/opt/fibe` without extra operator setup.
- The published Docker image pre-creates `/app/data` for the default SQLite/data paths. Operators must mount
  `/opt/fibe:/opt/fibe`; startup fails if the path is not writable or is not visible at the same path to Docker.
- The sibling `fibe-distilled-e2e` harness exposes its own test proxy for SDK/Playwright bootstrap compatibility.
  fibe-distilled itself does not contain `/e2e_backdoor/*` routes or fixture-marker runtime shims.

## Reporting a vulnerability

Please report security issues privately rather than opening a public issue. Email the maintainers (see the
repository owner / `github.com/fibegg`) with:

- a description of the issue and its impact,
- steps to reproduce or a proof of concept,
- the affected version/commit.

We will acknowledge the report and work on a fix; please allow reasonable time for remediation before any public
disclosure.
