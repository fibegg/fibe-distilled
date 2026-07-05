package worker

import (
	"net/http"
	"time"

	"github.com/fibegg/fibe-distilled/internal/runtime"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// HTTPDoer is the small HTTP client boundary used for routed readiness probes.
type HTTPDoer interface {
	// Do executes one HTTP request.
	Do(*http.Request) (*http.Response, error)
}

// Worker owns asynchronous fibe-distilled operations and runtime reconciliation.
type Worker struct {
	// DB is the SQLite repository boundary used by async work.
	DB *store.DB
	// Runtime is the local Docker/Compose control plane.
	Runtime runtime.Checker
	// DefaultGitHubToken is the process token used for GitHub source sync.
	DefaultGitHubToken string
	// RuntimeObserveTimeout bounds post-deploy runtime observation.
	RuntimeObserveTimeout time.Duration
	// RuntimeObserveInterval is the polling cadence during observation.
	RuntimeObserveInterval time.Duration
	// RouteProbeClient checks public routed service readiness when Docker has no health signal.
	RouteProbeClient HTTPDoer
}

// defaultBuildStaleTimeout is how long a duplicate deploy waits for an in-flight build.
const defaultBuildStaleTimeout = 45 * time.Minute

// defaultRuntimeObserveTimeout is how long deployment waits for runtime state.
const defaultRuntimeObserveTimeout = 90 * time.Second

// defaultRuntimeObserveInterval is the polling interval during runtime observation.
const defaultRuntimeObserveInterval = 2 * time.Second

// defaultRuntimeRepairCooldown suppresses repeated image-drift repairs.
const defaultRuntimeRepairCooldown = 10 * time.Minute
