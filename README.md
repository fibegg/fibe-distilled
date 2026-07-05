# fibe-distilled

fibe-distilled is a **minimal, self-hostable, single-tenant implementation of the [Fibe](https://fibe.gg) Platform**:
one Go binary (plus SQLite) that you spin up as a server and use to **deploy Playgrounds** — Docker Compose apps —
onto the same Docker host where fibe-distilled is running. It speaks enough of the Fibe HTTP API that the existing Fibe
SDK/CLI/MCP and a curated subset of the Playwright API suite work against it, rather than introducing a second
resource CLI.

It deliberately drops everything that makes Fibe a multi-tenant SaaS (UI, teams, billing, agents, templates, …) and
keeps just the create → launch → run loop. See [docs/LIMITATIONS.md](docs/LIMITATIONS.md) for the full scope,
security model, and known limitations.

## Requirements

- Go **1.26+** plus a C compiler when building from source outside Docker
- Docker Engine with the Compose plugin on the host running fibe-distilled
- writable `/opt/fibe` on that host
- access to `/var/run/docker.sock`

## Run

Canonical Docker run shape:

```sh
docker run -d --name fibe-distilled --restart unless-stopped \
  -p 2402:2402 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/fibe:/opt/fibe \
  -v fibe-distilled-data:/app/data \
  -e FIBE_API_KEY=dev-token \
  -e FIBE_ROOT_DOMAIN=apps.example.com \
  -e FIBE_ACME_EMAIL=ops@example.com \
  -e GITHUB_TOKEN=ghp_xxx \
  ghcr.io/fibegg/fibe-distilled
```

From source:

```sh
export FIBE_API_KEY=dev-token                 # required — the admin bearer token
export FIBE_ROOT_DOMAIN=apps.example.com      # required — routed Playground root domain
export FIBE_ACME_EMAIL=ops@example.com        # required — Let's Encrypt HTTP-01 account email
export FIBE_BUILD_PLATFORM=linux/amd64        # optional: linux/amd64 or linux/arm64
# GITHUB_TOKEN is OPTIONAL: only needed for GitHub repo status/write checks and private
# source sync/build labels in supplied Compose. Image-only playgrounds need no token.
export GITHUB_TOKEN=ghp_xxx                    # optional
# Optional receive-only GitHub push webhooks. Configure the same secret on GitHub.
export GITHUB_WEBHOOK_SECRET=webhook-secret     # optional
# Optional Docker Hub auth (avoids anonymous pull rate limits on the Marquee):
export DOCKERHUB_USERNAME=dockerhub-user
export DOCKERHUB_TOKEN=dockerhub-token

go run ./cmd/fibe-distilled
```

Defaults:

- `FIBE_ROOT_DOMAIN` must be a DNS hostname with at least one dot, for example `apps.example.com`; do
  not include `https://`, paths, ports, spaces, or multiple domains.
- The API always listens on `:2402`.
- `FIBE_DB_PATH=./data/fibe-distilled.sqlite3`
- `FIBE_DATA_DIR=./data`
- `FIBE_PLAYGUARD_INTERVAL_SECONDS=30` — the in-process reconcile loop tick; governs how fast expiration and
  runtime drift/repair are observed.

At startup fibe-distilled fails fast unless `/var/run/docker.sock` exists, Docker responds, Docker CLI and Compose are
available, `/opt/fibe` is writable and visible at the same path to the Docker daemon, and the managed Traefik container
can be started. HTTPS is always enabled. DNS for `FIBE_ROOT_DOMAIN` must already point to this host, and public Let's
Encrypt issuance requires the host to be reachable on HTTP/80 for HTTP-01. Traefik obtains certificates lazily when
routed Playgrounds are deployed.

Marquees are not user-managed resources in fibe-distilled. The API only exposes read-only discovery with
`GET /api/marquees` and `GET /api/marquees/:id`; create, update, delete, connection-test, legacy key-generation,
certificate, DNS, and Docker credential management calls return `NOT_IMPLEMENTED`.

`GET /up.json` is an unauthenticated health check. Every `/api/*` request needs `Authorization: Bearer <token>`. The
static `FIBE_API_KEY` is the admin credential (full access).

GitHub push webhooks are manual and receive-only. Set `GITHUB_WEBHOOK_SECRET`, then create a GitHub repository webhook
with payload URL `https://<fibe-distilled-host>/webhooks/github`, content type `application/json`, the same secret, and only
the `push` event. The endpoint does not use `FIBE_API_KEY`; it verifies `X-Hub-Signature-256`. `GITHUB_TOKEN` is still
only needed when fibe-distilled must read private repositories during source sync or image builds.

## Quick start — deploy a Playground

This is the whole point of fibe-distilled. With the server running on `localhost:2402`:

```sh
TOK='Authorization: Bearer dev-token'
BASE=http://localhost:2402

# 1. Inspect the startup-configured Marquee.
curl -s $BASE/api/marquees -H "$TOK"

# 2. Create a Playspec from a Compose document. fibe.gg/* labels declare exposure.
curl -s -X POST $BASE/api/playspecs -H "$TOK" -H 'Content-Type: application/json' -d '{
  "playspec": {
    "name": "hello",
    "base_compose_yaml": "services:\n  web:\n    image: nginx:alpine\n    labels:\n      fibe.gg/port: \"80\"\n      fibe.gg/subdomain: web\n"
  }
}'

# 3. Deploy a Playground for that Playspec on the configured Marquee. marquee_id can be omitted
#    or set to "default"; fibe-distilled resolves it to the startup-configured Marquee.
curl -s -X POST $BASE/api/playgrounds -H "$TOK" -H 'Content-Type: application/json' -d '{
  "playground": { "name": "hello-pg", "playspec_id": "hello" }
}'

# 4. Poll status until "running"; the response carries service_urls (e.g. https://web.apps.example.com).
curl -s $BASE/api/playgrounds/hello-pg/status -H "$TOK"

# 5. Tear it down when finished.
curl -s -X DELETE $BASE/api/playgrounds/hello-pg -H "$TOK"            # -> 202 {status: destroying}
```

Path parameters accept either the numeric ID or the resource name. Full Fibe Tricks/job-mode APIs are not implemented
in fibe-distilled; `job_mode`, `fibe.gg/job_watch`, `result_status`, and `/api/tricks` return explicit `NOT_IMPLEMENTED`
responses.

Runnable Compose examples live under [`examples/`](examples/):

- [`examples/hello-nginx`](examples/hello-nginx) is a smallest routed web Playground.

You can also drive fibe-distilled with the real Fibe SDK/CLI by pointing it at the server:

```sh
export FIBE_DOMAIN=http://localhost:2402
export FIBE_API_KEY=dev-token
```

## Implemented Minimal Scope

- `/up.json`, `/api/me`, `/api/status`, `/api/server-info`
- SQLite-backed configured Marquee metadata, Props, Playspecs, Playgrounds, BuildRecords, and async operations
- One static bearer token for `/api/*`
- Fibe-style list envelopes, request IDs, structured errors, and name-or-ID lookup
- Internal Compose checks inside Playspec/Launch writes plus service extraction for map/list labels, `x-fibe.gg` launch variables, service subdomains, env overrides, source mounts, and GitHub repo labels
- Runtime compose generation with Traefik labels, service URLs, `/opt/fibe/playgrounds/<project>` source/compose layout, exclusive managed-root cleanup, and per-playground Docker config
- Launch endpoint that creates a Playspec and optional Playground from caller-supplied Compose, with optional `repository_url` metadata for source-backed Props and GitHub write checks
- Dynamic BuildRecords backed by real local Docker builds for source-backed `build:` services
- Receive-only `POST /webhooks/github` for signed GitHub `push` events; matching source-mounted Playgrounds pull clean branches, while production/build services create latest BuildRecords and wait for manual rollout
- In-process Playguard loop for expiration enforcement, source sync, and runtime drift/repair
- Local Docker Compose deploy/stop/start/destroy through async-compatible Playground operations
- Runtime failure classification for Docker/Compose boundary errors
- Explicit `NOT_IMPLEMENTED` responses for excluded Fibe surfaces

## Deliberately Excluded

- Rails UI and session flows
- Managed Git-service provisioning (Gitea)
- Autonomous coding/runtime sidecars (agents)
- Multi-tenancy, teams, billing, API key CRUD, OAuth, GitHub Apps and webhook management
- Bazaar/templates/import template flows
- DNS-provider automation and uploaded TLS certificates
- Public Marquee management APIs; the local Marquee is configured by startup environment variables and exposed read-only

## Verify

```sh
prod_bin="$(mktemp)"
trap 'rm -f "$prod_bin"' EXIT
go build -tags sqlite_omit_load_extension -o "$prod_bin" ./cmd/fibe-distilled
go vet ./...
go test ./...
./bin/check
```

`./bin/check` is the normal contributor gate. It runs formatting checks, Go tests, `go vet`,
e2e-source isolation checks, `golangci-lint`, and `govulncheck`. It runs the
`docs/alloy` models when the `alloy` CLI is installed and skips that gate cleanly otherwise.

fibe-distilled does not contain docker-e2e backdoor routes or fixture shims. The sibling harness owns those compatibility
adapters outside the production source tree.

## Package Map And Godoc

[`PACKAGES.md`](PACKAGES.md) is the source-level architecture map. It documents each Go package, how packages
interact, how each package supports the main fibe-distilled goal, the dependency graph, and the local Godoc/helper-file
contract.

The `./bin/linters/godoc-contract` gate keeps that document tied to the code. It requires every production package to
have a `doc.go` package comment, every production function/method/type/const/var declaration to have a brief purpose
comment, every generated-doc exported member comment to follow Go's identifier-prefix convention, and every
package/dependency edge to be represented in `PACKAGES.md`. `helpers.go` is optional and used only for real local glue.

Generate browseable local docs with:

```sh
go tool -modfile=tools.mod godoc -http=:6060
```

## Docker E2E

Docker E2E orchestration lives in the sibling `fibe-distilled-e2e` repository. That keeps this repo focused on production
server code while still testing fibe-distilled as a blackbox image with the unchanged local Fibe SDK and Playwright subsets.

The harness runs seven roles: `fibe-distilled`, `e2e-proxy`, `dind` (the local Docker daemon provider), `sdk`, `playwright`,
`runtime-smoke`, and a `results` aggregator. The `fibe-distilled` image is built from this source tree without special
test tags; `e2e-proxy` handles the unchanged SDK/Playwright bootstrap endpoints outside fibe-distilled. The
`runtime-smoke` lane verifies the fibe-distilled-owned `/opt/fibe` artifact layout, Traefik secure router labels,
HTTP-to-HTTPS redirect, local SNI HTTPS routing, exclusive-root orphan cleanup, and local Marquee state over the Docker socket. Because the
Marquee is private to Docker, this does **not** prove public DNS reachability or real Let's Encrypt issuance.

The `sdk` and `playwright` lanes build from your local checkouts of the Fibe SDK and Playwright repositories, so set
their paths:

```sh
cd ../fibe-distilled-e2e
export FIBEDISTILLED_SDK_PATH=/path/to/fibe/sdk
export FIBEDISTILLED_PLAYWRIGHT_PATH=/path/to/fibe/playwright
export FIBEDISTILLED_APP_PATH=/path/to/fibe-distilled   # optional, defaults to ../fibe-distilled
```

Run the default subset through the wrapper:

```sh
./bin/docker-e2e
```

`./bin/docker-e2e` pins `COMPOSE_PROJECT_NAME=fibe-distilled-e2e`. Set `GITHUB_PAT` when you want GitHub repo-status or
source-backed supplied-Compose checks; if `GITHUB_TOKEN` is empty, the wrapper mirrors the PAT into `GITHUB_TOKEN`. Docker Hub auth is optional via
`DOCKERHUB_USERNAME`/`DOCKERHUB_TOKEN`.

The wrapper removes the e2e named volumes before each default run so SQLite rows, `/opt/fibe` runtime state, DinD state,
dummy bootstrap keys, and result files cannot leak between compatibility proofs. Set `FIBEDISTILLED_E2E_REUSE_STATE=1` only when intentionally
debugging a previous run's preserved state.

The default Playwright grep excludes the one provider-specific Prop CRUD assertion in
`tests/api/22-name-or-id-crud.spec.js`; the remaining name-or-ID tests stay in the default target list. Override
`PLAYWRIGHT_TEST_GREP` only when you intentionally want a narrower or different subset.

Narrow the SDK/Playwright suites without modifying those repositories via `GO_TEST_RUN`, `GO_TEST_RUNS`,
`PLAYWRIGHT_TEST_TARGETS`, and `PLAYWRIGHT_TEST_GREP`, e.g.:

```sh
GO_TEST_RUNS='TestPlaygrounds_CRUD;TestPlaygrounds_CreateWithServiceConfig' \
PLAYWRIGHT_TEST_TARGETS='tests/api/15-playground-lifecycle.spec.js' \
./bin/docker-e2e
```

## Release Automation

GitHub Actions provides:

- `CI`: runs `./bin/check` and a production Docker image build on pushes and pull requests.
- `Release`: on `v*` tags, publishes a Linux amd64 binary archive with `checksums.txt` and a multi-arch GHCR image.

Because fibe-distilled uses the official CGO SQLite driver, the binary release workflow publishes the Linux amd64 archive
from the native GitHub runner. The Docker image is the canonical multi-architecture artifact.

## Contributing & security

See [CONTRIBUTING.md](CONTRIBUTING.md) for the dev workflow and [SECURITY.md](SECURITY.md) for the trust model and
how to report a vulnerability.

## License

[MIT](LICENSE).
