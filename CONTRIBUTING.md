# Contributing to fibe-distilled

Thanks for your interest! fibe-distilled is the minimal, single-tenant Fibe runtime — a single Go binary for deploying
Docker Compose Playgrounds. Please keep changes aligned with [docs/LIMITATIONS.md](docs/LIMITATIONS.md); features
that belong to the full Fibe Platform (UI, multi-tenancy, agents, billing, templates, …) are intentionally out of
scope and should return `NOT_IMPLEMENTED`.

## Prerequisites

- Go **1.26+**
- CGO enabled for source builds and tests (`CGO_ENABLED=1`, the normal local default) because fibe-distilled uses the
  official SQLite driver.
- For the optional end-to-end harness: Docker + the Compose plugin, the sibling `fibe-distilled-e2e` repo, and local
  checkouts of the Fibe SDK and Playwright repos.

## Dev workflow

```sh
prod_bin="$(mktemp)"
trap 'rm -f "$prod_bin"' EXIT
go build -tags sqlite_omit_load_extension -o "$prod_bin" ./cmd/fibe-distilled
go vet ./...
gofmt -l .            # must print nothing
go test ./...         # unit + contract tests
./bin/check           # repo quality gates (runs docs/alloy models if `alloy` is installed)
```

All of the above must pass before sending a change. Add or update focused Go tests with any behavior change.

## Conventions

- Keep remote SSH command strings shell-quoted and validate any user-influenced path/identifier (project, branch,
  service) before it reaches a remote command.
- Docker-e2e bootstrap adapters live in the sibling `fibe-distilled-e2e` harness, not in fibe-distilled. The fake runtime
  executor lives in `internal/runtimetest` for Go tests only and must not be imported by production code.
- Preserve the Fibe-compatible HTTP shapes: `{data, meta}` list envelopes, `{error: {code, message, details}}`,
  `X-Request-Id`, async `202 + status_url` polling, and name-or-ID lookup.

## Optional end-to-end run

```sh
cd ../fibe-distilled-e2e
export FIBEDISTILLED_APP_PATH=/path/to/fibe-distilled # optional when fibe-distilled is ../fibe-distilled
export FIBEDISTILLED_SDK_PATH=/path/to/fibe/sdk
export FIBEDISTILLED_PLAYWRIGHT_PATH=/path/to/fibe/playwright
./bin/docker-e2e
```

Set `GITHUB_PAT` when you want GitHub fixture tests; the wrapper mirrors it to `GITHUB_TOKEN` if needed. Without it,
those fixture tests skip and the rest of the server/API subset still runs.

The wrapper starts from clean e2e volumes by default. Set `FIBEDISTILLED_E2E_REUSE_STATE=1` only when intentionally
debugging a preserved Docker-in-Docker, SQLite, or result-file state from a prior run.
