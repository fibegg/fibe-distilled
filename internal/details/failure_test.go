package details

import "testing"

func TestClassifyComposeFailure(t *testing.T) {
	tests := map[string]struct {
		message  string
		expected string
	}{
		"compose schema validation": {
			message:  "docker compose up failed: exit status 1: validating /opt/fibe/playgrounds/demo/compose.yml: services.kafka.volumes must be a array",
			expected: "invalid_compose",
		},
		"port conflict": {
			message:  "Bind for 0.0.0.0:8080 failed: port is already allocated",
			expected: "port_bind",
		},
		"dependency unhealthy": {
			message:  `dependency failed to start: container demo-db-1 is unhealthy`,
			expected: "dependency_unhealthy",
		},
		"docker hub rate limit": {
			message:  "Error response from daemon: toomanyrequests: You have reached your unauthenticated pull rate limit",
			expected: "registry_rate_limited",
		},
		"docker pull auth": {
			message:  "Error response from daemon: Head https://registry-1.docker.io/v2/acme/private/manifests/latest: unauthorized: authentication required",
			expected: "image_pull",
		},
	}
	for name, tt := range tests {
		tt := tt
		t.Run(name, func(t *testing.T) {
			got := ClassifyComposeFailure(tt.message, []string{"db"}).Category
			if got != tt.expected {
				t.Fatalf("category = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestClassifyComposeFailureIncludesDockerPullSubtype(t *testing.T) {
	diagnostics := ClassifyComposeFailure("pull failed: no basic auth credentials", nil)
	if diagnostics.Category != "image_pull" || diagnostics.DockerPullErrorType != "auth_failed" {
		t.Fatalf("unexpected docker pull diagnostics: %#v", diagnostics)
	}
	details := diagnostics.Details()
	compose, ok := details["compose_failure"].(map[string]any)
	if !ok || compose["docker_pull_error_type"] != "auth_failed" {
		t.Fatalf("expected docker pull subtype in details: %#v", details)
	}
}

func TestClassifyComposeFailureRecognizesRequestedAccessDeniedPull(t *testing.T) {
	diagnostics := ClassifyComposeFailure("pull access denied for acme/private, repository does not exist or may require 'docker login': denied: requested access to the resource is denied", nil)
	if diagnostics.Category != "image_pull" || diagnostics.DockerPullErrorType != "auth_failed" {
		t.Fatalf("unexpected docker pull diagnostics: %#v", diagnostics)
	}
}

func TestClassifyComposeFailureRecognizesManifestNotFoundPull(t *testing.T) {
	diagnostics := ClassifyComposeFailure("failed to solve: nginx:nope: manifest not found", nil)
	if diagnostics.Category != "image_pull" || diagnostics.DockerPullErrorType != "not_found" {
		t.Fatalf("unexpected docker pull diagnostics: %#v", diagnostics)
	}
}

func TestClassifyComposeFailureDoesNotInferDockerPullSubtype(t *testing.T) {
	tests := map[string]struct {
		message      string
		wantCategory string
	}{
		"permission denied": {
			message:      "docker compose failed: permission denied while mounting /opt/fibe",
			wantCategory: "permission",
		},
		"network not found": {
			message:      "Error response from daemon: network fibe-distilled_default not found",
			wantCategory: "network",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			diagnostics := ClassifyComposeFailure(tt.message, nil)
			if diagnostics.Category != tt.wantCategory {
				t.Fatalf("category = %q, want %q", diagnostics.Category, tt.wantCategory)
			}
			if diagnostics.DockerPullErrorType != "" {
				t.Fatalf("plain %s failure should not have docker pull subtype: %#v", tt.wantCategory, diagnostics)
			}
		})
	}
}

func TestClassifyComposeFailureFindsServiceFromComposeContainerName(t *testing.T) {
	diagnostics := ClassifyComposeFailure(`container "demo-api-worker-1" exited (1)`, []string{"api", "api-worker"})
	if diagnostics.FailedService != "api-worker" {
		t.Fatalf("failed service = %q, want api-worker", diagnostics.FailedService)
	}
}

func TestRetryableInfrastructureFailureExcludesCloneFailure(t *testing.T) {
	if retryableInfrastructureFailure("clone failed: connection refused") {
		t.Fatal("clone failures should not be treated as retryable infrastructure")
	}
	if !retryableInfrastructureFailure("ssh connection refused") {
		t.Fatal("plain transport failures should be retryable infrastructure")
	}
}

func TestRetryableInfrastructureFailureExcludesPermissionDenied(t *testing.T) {
	diagnostics := ClassifyComposeFailure("docker compose failed: permission denied while mounting /opt/fibe", nil)
	if diagnostics.RetryableInfrastructure {
		t.Fatalf("permission problems require configuration changes, got retryable diagnostics: %#v", diagnostics)
	}
	if details := diagnostics.Details(); details["retryable_infrastructure"] != nil {
		t.Fatalf("permission details should not expose retryable_infrastructure: %#v", details)
	}
}
