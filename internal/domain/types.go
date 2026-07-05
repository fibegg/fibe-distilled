package domain

import "time"

// Playground and async status constants mirror the compatible Fibe API states.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusRunning    = "running"
	StatusError      = "error"
	StatusHasChanges = "has_changes"
	StatusCompleted  = "completed"
	StatusStopped    = "stopped"
	StatusStopping   = "stopping"
	StatusDestroying = "destroying"

	AsyncQueued  = "queued"
	AsyncRunning = "running"
	AsyncSuccess = "success"
	AsyncError   = "error"

	BuildStatusPending  = "pending"
	BuildStatusBuilding = "building"
	BuildStatusSuccess  = "success"
	BuildStatusFailed   = "failed"
)

// Marquee describes the synthetic local Docker host fibe-distilled deploys to.
type Marquee struct {
	// ID is the persisted Marquee identifier.
	ID int64 `json:"id"`
	// Name is the SDK-visible configured Marquee name.
	Name string `json:"name"`
	// Host is the SDK-compatible local host value.
	Host string `json:"host"`
	// Port is the SDK-compatible local port value.
	Port int `json:"port"`
	// User is the SDK-compatible local user value.
	User string `json:"user"`
	// Status is the runtime availability status exposed to clients.
	Status string `json:"status"`
	// DomainsInput is the configured root-domain string.
	DomainsInput *string `json:"domains_input"`
	// HTTPSEnabled is always true for the startup-configured Marquee.
	HTTPSEnabled *bool `json:"https_enabled,omitempty"`
	// TLSCertificateSource reports the automatic Let's Encrypt mode.
	TLSCertificateSource *string `json:"tls_certificate_source,omitempty"`
	// AcmeEmail is the Let's Encrypt account email.
	AcmeEmail *string `json:"acme_email"`
	// BuildPlatform is the optional Docker build platform.
	BuildPlatform *string `json:"build_platform"`
	// RuntimeLaunchable preserves the SDK Marquee-inference response shape.
	RuntimeLaunchable bool `json:"billing_runtime_active"`
	// ChatLaunchable preserves the Fibe response shape.
	ChatLaunchable bool `json:"chat_launchable"`
	// CreatedAt is the persisted creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the persisted update timestamp.
	UpdatedAt time.Time `json:"updated_at"`

	// SSHPrivateKey is retained only for legacy SQLite compatibility and is never serialized.
	SSHPrivateKey string `json:"-"`
}

// Prop describes a source repository known to fibe-distilled.
type Prop struct {
	// ID is the persisted Prop identifier.
	ID int64 `json:"id"`
	// Name is the SDK-visible Prop name.
	Name string `json:"name"`
	// RepositoryURL is the normalized source repository URL.
	RepositoryURL string `json:"repository_url"`
	// Private reports whether the repository requires credentials.
	Private bool `json:"private"`
	// DefaultBranch is the repository default branch.
	DefaultBranch string `json:"default_branch"`
	// Status is the Prop sync/status value.
	Status string `json:"status"`
	// Provider is the supported source provider label.
	Provider string `json:"provider"`
	// LastSyncedAt records the last successful branch sync time.
	LastSyncedAt *time.Time `json:"last_synced_at"`
	// CreatedAt is the persisted creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the persisted update timestamp.
	UpdatedAt time.Time `json:"updated_at"`
	// Branches is the compact branch-name list for API compatibility.
	Branches []string `json:"branches,omitempty"`
	// BranchRecords stores enriched branch metadata inside the Prop row.
	BranchRecords []PropBranch `json:"-"`
}

// PropBranch describes a discovered repository branch.
type PropBranch struct {
	// Name is the branch name.
	Name string `json:"name"`
	// Default reports whether this is the repository default branch.
	Default bool `json:"default"`
	// HeadSHA is the branch head commit SHA.
	HeadSHA string `json:"head_sha,omitempty"`
	// LastSyncedAt records the last successful branch sync.
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
}

// Playspec stores a named base Compose document and its API metadata.
type Playspec struct {
	// ID is the persisted Playspec identifier.
	ID *int64 `json:"id"`
	// Name is the SDK-visible Playspec name.
	Name string `json:"name"`
	// Description is optional user-facing metadata.
	Description *string `json:"description"`
	// Locked reports whether dependent Playgrounds prevent destructive changes.
	Locked *bool `json:"locked"`
	// PersistVolumes remains false for fibe-distilled's stateless runtime.
	PersistVolumes *bool `json:"persist_volumes"`
	// PlaygroundCount is the number of dependent Playgrounds.
	PlaygroundCount *int64 `json:"playground_count"`
	// CreatedAt is the persisted creation timestamp.
	CreatedAt *time.Time `json:"created_at"`
	// UpdatedAt is the persisted update timestamp.
	UpdatedAt *time.Time `json:"updated_at"`
	// Services is the parsed service summary payload.
	Services []any `json:"services,omitempty"`

	// BaseComposeYAML is the stored Compose document and is serialized separately.
	BaseComposeYAML string `json:"-"`
}

// Playground is a runtime instance of a Playspec.
type Playground struct {
	// ID is the persisted Playground identifier.
	ID int64 `json:"id"`
	// Name is the SDK-visible Playground name.
	Name string `json:"name"`
	// Status is the current lifecycle state.
	Status string `json:"status"`
	// StateReason is the primary reason for the current lifecycle state.
	StateReason *string `json:"state_reason,omitempty"`
	// StateReasons carries additional lifecycle reasons.
	StateReasons []string `json:"state_reasons,omitempty"`
	// PlayspecID links the Playground to its base Playspec.
	PlayspecID *int64 `json:"playspec_id"`
	// PlayspecName is denormalized for client display.
	PlayspecName *string `json:"playspec_name"`
	// MarqueeID links the Playground to the configured Marquee.
	MarqueeID *int64 `json:"marquee_id,omitempty"`
	// ServiceBranches stores selected branches by service.
	ServiceBranches map[string]any `json:"service_branches"`
	// ExpiresAt is the optional destruction time.
	ExpiresAt *time.Time `json:"expires_at"`
	// CreatedAt is the persisted creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the persisted update timestamp.
	UpdatedAt time.Time `json:"updated_at"`
	// ComposeProject is the deterministic Docker Compose project name.
	ComposeProject *string `json:"compose_project,omitempty"`
	// MarqueeName is denormalized for client display.
	MarqueeName *string `json:"marquee_name,omitempty"`
	// RootDomain is the Marquee root domain used for routes.
	RootDomain *string `json:"root_domain,omitempty"`
	// RoutingScheme is the generated URL scheme.
	RoutingScheme *string `json:"routing_scheme,omitempty"`
	// InternalPassword is the default Basic Auth password for internal routes.
	InternalPassword *string `json:"internal_password,omitempty"`
	// EnvOverrides are launch/update environment overrides.
	EnvOverrides map[string]string `json:"env_overrides,omitempty"`
	// LastAppliedAt records when runtime Compose was last applied.
	LastAppliedAt *time.Time `json:"last_applied_at,omitempty"`
	// ErrorMessage is the current user-facing failure summary.
	ErrorMessage *string `json:"error_message,omitempty"`
	// ErrorDetails carries structured runtime failure evidence.
	ErrorDetails map[string]any `json:"error_details,omitempty"`
	// BuildWarnings contains advisory build/render warnings.
	BuildWarnings []string `json:"build_warnings,omitempty"`
	// BuildStatuses summarizes dynamic build records by service.
	BuildStatuses []PlaygroundBuildStatus `json:"build_statuses,omitempty"`
	// ServiceURLs lists generated routed service URLs.
	ServiceURLs []PlaygroundServiceURL `json:"service_urls,omitempty"`
	// Services lists rendered or observed service state.
	Services []PlaygroundServiceInfo `json:"services,omitempty"`
	// GeneratedComposeYAML is the deployed runtime Compose document.
	GeneratedComposeYAML string `json:"-"`
	// CreationSteps records user-visible deploy progress.
	CreationSteps []PlaygroundCreationStep `json:"creation_steps,omitempty"`
	// PlayguardRepairReason records the last repair reason.
	PlayguardRepairReason *string `json:"playguard_repair_reason,omitempty"`
	// PlayguardRepairLockUntil throttles repeated safe repair attempts.
	PlayguardRepairLockUntil *time.Time `json:"playguard_repair_lock_until,omitempty"`
	// NeedsRecreation asks clients to redeploy rather than trust current state.
	NeedsRecreation *bool `json:"needs_recreation,omitempty"`
	// TimeRemaining is the expiration countdown in seconds.
	TimeRemaining *float64 `json:"time_remaining,omitempty"`
	// ExpirationPercentage is the elapsed fraction of the Playground lifetime.
	ExpirationPercentage *float64 `json:"expiration_percentage,omitempty"`
}

// PlaygroundCreationStep records user-visible deployment progress.
type PlaygroundCreationStep struct {
	// Name is the stable step key.
	Name string `json:"name"`
	// Label is the human-readable step label.
	Label string `json:"label,omitempty"`
	// Status is the step lifecycle state.
	Status string `json:"status"`
	// StartedAt records when the step began.
	StartedAt *time.Time `json:"started_at,omitempty"`
	// CompletedAt records when the step finished.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// ErrorMessage records the step failure summary.
	ErrorMessage string `json:"error_message,omitempty"`
}

// PlaygroundServiceURL describes an HTTP route generated for a Playground service.
type PlaygroundServiceURL struct {
	// Name is the service name.
	Name string `json:"name"`
	// Type is the URL classification used by Fibe clients.
	Type string `json:"type,omitempty"`
	// URL is the absolute routed service URL.
	URL string `json:"url"`
	// Visibility is the public/internal route visibility.
	Visibility string `json:"visibility,omitempty"`
	// AuthRequired reports whether Basic Auth protects the route.
	AuthRequired bool `json:"auth_required"`
	// Status is the observed route/service lifecycle state.
	Status string `json:"status,omitempty"`
	// Health is the observed Docker health value.
	Health string `json:"health,omitempty"`
	// Running reports whether Docker considers the service running.
	Running *bool `json:"running,omitempty"`
	// ExitCode records a terminal container exit code when available.
	ExitCode *int `json:"exit_code,omitempty"`
}

// PlaygroundServiceInfo describes observed or rendered runtime service state.
type PlaygroundServiceInfo struct {
	// Name is the service name.
	Name string `json:"name"`
	// Status is the observed or rendered service state.
	Status string `json:"status"`
	// Image is the effective service image.
	Image string `json:"image,omitempty"`
	// Health is the Docker health status.
	Health string `json:"health,omitempty"`
	// Running reports whether the service container is running.
	Running bool `json:"running,omitempty"`
	// ExitCode records a terminal container exit code when available.
	ExitCode *int `json:"exit_code,omitempty"`
}

// PlaygroundBuildStatus groups build records for one Playground service.
type PlaygroundBuildStatus struct {
	// ServiceName is the service the records belong to.
	ServiceName string `json:"service_name"`
	// PropID is the source Prop identifier when known.
	PropID int64 `json:"prop_id,omitempty"`
	// PropName is the source Prop name when known.
	PropName string `json:"prop_name,omitempty"`
	// Branch is the source branch built for the service.
	Branch string `json:"branch,omitempty"`
	// Running is the currently running build record.
	Running *PlaygroundBuildRecordSnapshot `json:"running,omitempty"`
	// Latest is the most recent build record.
	Latest *PlaygroundBuildRecordSnapshot `json:"latest,omitempty"`
	// Active is the build record whose image is deployed.
	Active *PlaygroundBuildRecordSnapshot `json:"active,omitempty"`
}

// PlaygroundBuildRecordSnapshot is the API-sized view of a BuildRecord.
type PlaygroundBuildRecordSnapshot struct {
	// ID is the BuildRecord identifier.
	ID int64 `json:"id"`
	// ServiceName is the service that was built.
	ServiceName string `json:"service_name,omitempty"`
	// Status is the build lifecycle state.
	Status string `json:"status"`
	// CommitSHA is the source commit used by the build.
	CommitSHA string `json:"commit_sha"`
	// ShortCommitSHA is a display-sized commit SHA.
	ShortCommitSHA string `json:"short_commit_sha,omitempty"`
	// ImageRef is the deterministic image tag.
	ImageRef string `json:"image_ref,omitempty"`
	// ErrorMessage records the build failure summary.
	ErrorMessage string `json:"error_message,omitempty"`
	// StartedAt records when the build started.
	StartedAt *time.Time `json:"started_at,omitempty"`
	// CompletedAt records when the build finished.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// CreatedAt records when the BuildRecord was created.
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

// BuildRecord stores one dynamic image build attempt for a Playground service.
type BuildRecord struct {
	// ID is the persisted BuildRecord identifier.
	ID int64 `json:"id"`
	// PlaygroundID links the build to a Playground.
	PlaygroundID *int64 `json:"playground_id,omitempty"`
	// PropID links the build to a source Prop.
	PropID *int64 `json:"prop_id,omitempty"`
	// ServiceName is the service being built.
	ServiceName string `json:"service_name"`
	// Branch is the source branch being built.
	Branch string `json:"branch"`
	// CommitSHA is the source commit used by the build.
	CommitSHA string `json:"commit_sha"`
	// Status is the build lifecycle state.
	Status string `json:"status"`
	// ImageRef is the deterministic image tag.
	ImageRef string `json:"image_ref,omitempty"`
	// BuildDockerfilePath is the Dockerfile path used by the build.
	BuildDockerfilePath string `json:"build_dockerfile_path,omitempty"`
	// BuildTarget is the Docker build target.
	BuildTarget string `json:"build_target,omitempty"`
	// BuildArgsDigest identifies normalized build arguments.
	BuildArgsDigest string `json:"build_args_digest,omitempty"`
	// BuildIdentityDigest identifies build-affecting inputs.
	BuildIdentityDigest string `json:"build_identity_digest,omitempty"`
	// BuildPlatform is the Docker build platform.
	BuildPlatform string `json:"build_platform,omitempty"`
	// BuildCacheKey records the cache identity fibe-distilled calculated.
	BuildCacheKey string `json:"build_cache_key,omitempty"`
	// Reused reports whether the build reused a cached image.
	Reused bool `json:"reused,omitempty"`
	// Logs contains the local build logs.
	Logs string `json:"logs,omitempty"`
	// ErrorMessage records the build failure summary.
	ErrorMessage *string `json:"error_message,omitempty"`
	// StartedAt records when the build started.
	StartedAt *time.Time `json:"started_at,omitempty"`
	// CompletedAt records when the build finished.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// CreatedAt is the persisted creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the persisted update timestamp.
	UpdatedAt time.Time `json:"updated_at"`
}

// PlaygroundStatus is the status-polling response returned to SDK clients.
type PlaygroundStatus struct {
	// ID is the Playground identifier.
	ID int64 `json:"id"`
	// Name is the Playground name.
	Name string `json:"name"`
	// Status is the Playground lifecycle state.
	Status string `json:"status"`
	// CreationStep is the current deployment step key.
	CreationStep string `json:"creation_step"`
	// CreationStepLabel is the current deployment step label.
	CreationStepLabel string `json:"creation_step_label,omitempty"`
	// ErrorStep is the failed deployment step key.
	ErrorStep string `json:"error_step,omitempty"`
	// ErrorStepLabel is the failed deployment step label.
	ErrorStepLabel string `json:"error_step_label,omitempty"`
	// CreationSteps is the full deployment progress list.
	CreationSteps []PlaygroundCreationStep `json:"creation_steps,omitempty"`
	// Services is the observed service state list.
	Services []PlaygroundServiceInfo `json:"services"`
	// ServiceURLs is the routed URL list.
	ServiceURLs []PlaygroundServiceURL `json:"service_urls,omitempty"`
	// BuildStatuses is the dynamic build summary list.
	BuildStatuses []PlaygroundBuildStatus `json:"build_statuses,omitempty"`
	// ErrorMessage is the user-facing failure summary.
	ErrorMessage *string `json:"error_message"`
	// ErrorDetails carries structured runtime failure evidence.
	ErrorDetails map[string]any `json:"error_details,omitempty"`
	// NeedsRecreation reports whether a redeploy is required.
	NeedsRecreation bool `json:"needs_recreation"`
	// ExpiresAt is the optional destruction time.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// TimeRemaining is the expiration countdown in seconds.
	TimeRemaining *float64 `json:"time_remaining,omitempty"`
	// ExpirationPercentage is the elapsed fraction of the Playground lifetime.
	ExpirationPercentage *float64 `json:"expiration_percentage,omitempty"`
	// UpdatedAt is the status update timestamp.
	UpdatedAt time.Time `json:"updated_at"`
}

// AsyncOperation tracks a background API operation and its eventual payload.
type AsyncOperation struct {
	// ID is the async request identifier.
	ID string `json:"request_id"`
	// Status is queued, running, success, or error.
	Status string `json:"status"`
	// StatusURL is the polling URL returned to clients.
	StatusURL string `json:"status_url,omitempty"`
	// Payload is the terminal success payload.
	Payload map[string]any `json:"-"`
	// Error is the terminal structured failure payload.
	Error *APIError `json:"error,omitempty"`
	// CreatedAt is the persisted creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the persisted update timestamp.
	UpdatedAt time.Time `json:"updated_at"`
}

// APIError is the stable structured error shape shared by HTTP and async APIs.
type APIError struct {
	// Code is the stable machine-readable error code.
	Code string `json:"code"`
	// Message is the human-readable error summary.
	Message string `json:"message"`
	// Details carries structured error context.
	Details map[string]any `json:"details,omitempty"`
}
