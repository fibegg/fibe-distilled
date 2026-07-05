# fibe-distilled — trust model and scoped limitations

fibe-distilled is the **minimal** Fibe: a single-binary, single-tenant server for deploying Docker Compose Playgrounds
onto the local Docker host mounted into the fibe-distilled container. This document lists the trust model and limitations inside
that chosen runtime scope; intentionally unsupported Fibe product areas are not repeated here.

## 1. Trust model (read first)

fibe-distilled is **single-tenant and single-operator**. The design assumes:

- **One admin.** `FIBE_API_KEY` is a single, full-access admin bearer token. There is no token minting,
  token scope persistence, user/team model, login, 2FA, or API-key CRUD. Treat the admin token like a root password.
  It is compared in constant time to avoid timing oracles.
- **You author your own Playspecs.** A Playspec is arbitrary Docker Compose that fibe-distilled runs on your Marquee. It
  is **not** sandboxed (see §2.1). Only deploy compose you trust onto a host you control.
- **One instance exclusively owns its local Marquee.** The configured Marquee and
  `/opt/fibe/playgrounds` root are treated as fibe-distilled-managed. Known Playground lifecycle operations still validate
  the compose project/path before destructive work, and Playguard removes safe `/opt/fibe/playgrounds/<project>`
  directories that are no longer represented by SQLite. Do not point two fibe-distilled instances or non-fibe-distilled
  automation at the same physical Docker host expecting them to share ownership.
- **The Docker socket is the trust boundary.** `/var/run/docker.sock` gives fibe-distilled root-equivalent control over the
  host. The official image runs as root and startup fails unless `/opt/fibe` is writable and bind-mounted at the same
  path the Docker daemon sees.

## 2. Accepted limitations (won't fix in the minimal scope)

### 2.1 Compose is not hardened / sandboxed
`POST /api/playspecs` and `/api/launches` validate `fibe.gg/*` labels and structure, but do **not** reject
container-escape primitives — `privileged: true`, `cap_add`, `network_mode: host`, `pid: host`, `devices`, or host
bind mounts (including `/var/run/docker.sock`). A Playspec author can therefore take over the Docker host. This is
acceptable for the single-operator model (you author your own playspecs onto your own host) but means **fibe-distilled
must not be exposed as a multi-tenant service** where untrusted users submit compose. Fibe's full platform performs
this isolation; fibe-distilled does not.

### 2.1b Stateless model & strict request gate
fibe-distilled Playgrounds/Playspecs are **stateless**. A request compatibility gate (`internal/compatgate`) runs after
auth on every `/api/*` request and returns a structured `NOT_IMPLEMENTED` (with `details`) for anything outside the
minimal runtime contract, including:

- **Unknown/extra JSON fields** on supported request bodies — fibe-distilled enforces its documented contract rather than
  silently ignoring fields a fuller Fibe would accept.
- **`persist_volumes`** on Playspecs/Playgrounds — persistent-volume (stateful) mode is not supported.
- **`fibe.gg/zerodowntime`** and healthcheck override labels — outside the stateless runtime scope.

### 2.2 Concurrency model for playground updates
SQLite runs with `journal_mode=WAL` and `busy_timeout=5000`, and `SetMaxOpenConns(1)` serializes individual
statements. Playground PATCH, expiration, operation, deploy-progress, refresh, and expiration paths re-read the
current row at their lifecycle boundary so stale workers do not blindly overwrite user-visible metadata. Effective
runtime configuration edits to active playgrounds move the row to `has_changes`, which tells clients a rollout is
required and gives in-flight deploy workers a clear supersession signal; same-value updates stay no-ops. Full
optimistic concurrency across every JSON field is still out of scope for the single-process runtime.

### 2.3 Deploy is synchronous
`POST /api/playgrounds` (and the launch path) renders compose, syncs source, builds, runs `docker compose up`, and
observes runtime **inline**, then returns the final playground (or an error). This matches the SDK's direct-return
create contract and lets failures surface immediately. For large images/builds the call can take minutes — set a
generous client/SDK timeout, or create then poll `GET /api/playgrounds/{id}/status`. The deploy runs on a context
detached from the request, so a client timeout does **not** cancel or orphan an in-flight deploy; the server
finishes it and you can re-fetch the playground by name.

### 2.4 HTTPS / DNS
HTTPS Traefik labels (Let's Encrypt HTTP-01) are always generated for the configured local Marquee. There is no
DNS-provider automation and no uploaded-certificate support; you must point DNS at the Docker host yourself. The
`fibe-distilled-e2e` uses a Docker-private Marquee, so it does not prove public DNS reachability or real certificate
issuance.

### 2.5 Source, templates, and env files
Source-backed Playgrounds are supported only through Compose behavior fibe-distilled actually executes:

- Prop provider selectors are limited to `github` and generic `git`. Other provider strings are rejected even when
  their repository URLs are otherwise valid.
- Generic Git Prop sync is a read-only no-op. Runtime source sync/build still uses the configured branch when
  deploying, but the API server does not discover generic remote refs or fabricate branch sync timestamps.
- Dynamic source/build behavior must be declared in Compose through `build:`, `fibe.gg/repo_url`, and related
  `fibe.gg/*` labels. Full-Fibe Playspec service-classification fields such as `services[].prop_id`,
  `services[].workdir`, and `services[].workflow` are rejected.
- Direct `/api/playgrounds` creation cannot compile template variables from an existing Playspec because that SDK
  payload has no `variables` field. Use `/api/launches` with variables so fibe-distilled compiles `x-fibe.gg.variables`,
  `$$var__*`, `$$random__*`, and `$$root_domain` before deployment.
- Compose `env_file` is rejected even though Docker Compose supports it. fibe-distilled uploads generated Compose and
  managed source checkouts, but it does not fetch, upload, parse, or resolve separate env files; use explicit launch
  `env_overrides` or Compose `environment`.

## 3. Operational notes

- **`GITHUB_TOKEN` is optional.** It is only used for GitHub repo status/write checks and private source sync/build
  labels in supplied Compose. fibe-distilled does not fetch a Compose config from GitHub repository refs. Image-only
  playgrounds — the core use case — need no token.
- **GitHub source-sync auth is not persisted, but is transient on the runtime host.** Source-sync diagnostics redact
  credentialed URLs and Authorization headers, and fibe-distilled resets Git remotes to non-credentialed URLs after
  clone/update. During the Git operation, the Authorization header is still present in the fibe-distilled process
  environment; privileged same-host process inspection may observe it while source sync is running.
- **The configured root domain is required.** `FIBE_ROOT_DOMAIN` is mandatory at startup and is used for exposed
  (`fibe.gg/port`) service URLs. DNS must already point that domain at the Docker host.
- **Branch / path inputs are sanitized.** User-controlled branch names are sanitized to a safe path component
  (dots/dashes trimmed) so a value like `..` can never traverse out of the per-branch source directory; runtime
  command strings are shell-quoted, and compose project names are validated before any teardown.
- **DockerHub credentials**, if provided, are written to a per-playground `docker-config/config.json` under `/opt/fibe`
  with `chmod 600`. They are your own credentials on your own host; still, anyone with host access could read them.
- **Local source builds use CGO.** The production Docker build is canonical and installs the C toolchain in the
  builder stage. If you build outside Docker, install a C compiler because fibe-distilled uses the official
  `go-sqlite3` driver.
