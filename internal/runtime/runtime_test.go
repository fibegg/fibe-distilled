package runtime_test

import (
	"context"
	"encoding/base64"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
)

func TestDeployComposeWritesFibeHostArtifactsAndUsesRuntimeFlags(t *testing.T) {
	fake := &runtimetest.FakeExecutor{}
	checker := runtime.Checker{Executor: fake, DockerHubUsername: "dock-user", DockerHubToken: "dock-token", InstanceID: "server-1"}
	marquee := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22}

	err := checker.DeployCompose(context.Background(), marquee, "demo--42", 42, "services:\n  web:\n    image: nginx\n")
	if err != nil {
		t.Fatalf("deploy compose: %v", err)
	}

	seen := strings.Join(fake.Seen, "\n")
	for _, want := range []string{
		"mkdir -p '/opt/fibe/playgrounds/demo--42/props'",
		"mkdir -p '/opt/fibe/playgrounds/demo--42/docker-config'",
		"mkdir -p '/opt/fibe/builds'",
		"write:/opt/fibe/playgrounds/demo--42/compose.yml:",
		"write:/opt/fibe/playgrounds/demo--42/docker-config/config.json:",
		`"https://index.docker.io/v1/"`,
		`"registry-1.docker.io"`,
		base64.StdEncoding.EncodeToString([]byte("dock-user:dock-token")),
		"DOCKER_CONFIG='/opt/fibe/playgrounds/demo--42/docker-config' docker compose -f compose.yml -p 'demo--42' up -d --remove-orphans --pull missing",
	} {
		if !strings.Contains(seen, want) {
			t.Fatalf("expected %q in runtime calls:\n%s", want, seen)
		}
	}
	if strings.Contains(seen, "--build") {
		t.Fatalf("standard compose deploy should not bypass BuildRecords with --build:\n%s", seen)
	}
	assertRecordedShellCommandsParse(t, fake)
}

func TestPreparePlaygroundWorkspaceWritesRuntimeFiles(t *testing.T) {
	fake := &runtimetest.FakeExecutor{}
	checker := runtime.Checker{Executor: fake, DockerHubUsername: "dock-user", DockerHubToken: "dock-token", InstanceID: "server-1"}
	marquee := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22}

	err := checker.PreparePlaygroundWorkspace(context.Background(), marquee, "demo--42", 42)
	if err != nil {
		t.Fatalf("prepare playground workspace: %v", err)
	}

	seen := strings.Join(fake.Seen, "\n")
	for _, want := range []string{
		"mkdir -p '/opt/fibe/playgrounds/demo--42/props'",
		"mkdir -p '/opt/fibe/playgrounds/demo--42/docker-config'",
		"mkdir -p '/opt/fibe/builds'",
		"write:/opt/fibe/playgrounds/demo--42/docker-config/config.json:",
	} {
		if !strings.Contains(seen, want) {
			t.Fatalf("expected %q in runtime calls:\n%s", want, seen)
		}
	}
	for _, unexpected := range []string{"write:/opt/fibe/playgrounds/demo--42/compose.yml:", "docker compose -f compose.yml"} {
		if strings.Contains(seen, unexpected) {
			t.Fatalf("workspace preparation should not deploy Compose, saw %q in:\n%s", unexpected, seen)
		}
	}
	assertRecordedShellCommandsParse(t, fake)
}

func TestListPlaygroundProjectsReturnsSafeProjectDirectories(t *testing.T) {
	marquee := domain.Marquee{ID: 7, Name: "local"}
	fake := &runtimetest.FakeExecutor{
		ReadFiles: map[string]string{
			optfibe.PlaygroundPath("owned--1") + "/compose.yml":   "services:\n  web:\n    image: nginx\n",
			optfibe.PlaygroundPath("another--1") + "/compose.yml": "services:\n  web:\n    image: nginx\n",
			optfibe.PlaygroundsPath + "/../unsafe/compose.yml":    "services:\n  web:\n    image: nginx\n",
			optfibe.PlaygroundPath("-flag") + "/compose.yml":      "services:\n  web:\n    image: nginx\n",
			optfibe.PlaygroundPath("UPPERCASE") + "/compose.yml":  "services:\n  web:\n    image: nginx\n",
		},
	}
	checker := runtime.Checker{Executor: fake, InstanceID: "server-1"}

	projects, err := checker.ListPlaygroundProjects(context.Background(), marquee)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	want := []string{"another--1", "owned--1"}
	if strings.Join(projects, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected projects: got %#v want %#v", projects, want)
	}
}

func TestEnsurePrerequisitesChecksFibeRuntimeDirectories(t *testing.T) {
	fake := &runtimetest.FakeExecutor{}
	checker := runtime.Checker{Executor: fake}
	if err := checker.EnsurePrerequisites(context.Background(), domain.Marquee{Name: "local"}); err != nil {
		t.Fatalf("ensure prerequisites: %v", err)
	}
	seen := strings.Join(fake.Seen, "\n")
	for _, want := range []string{
		"docker:ping",
		"mkdir -p '/opt/fibe/playgrounds'",
		"mkdir -p '/opt/fibe/traefik'",
		"mkdir -p '/opt/fibe/builds'",
	} {
		if !strings.Contains(seen, want) {
			t.Fatalf("expected %q in connection checks:\n%s", want, seen)
		}
	}
}

func TestDeployComposeEnsuresTraefikWhenMarqueeHasDomain(t *testing.T) {
	fake := &runtimetest.FakeExecutor{}
	checker := runtime.Checker{
		Executor:          fake,
		DockerHubUsername: "dock-user",
		DockerHubToken:    "dock-token",
		InstanceID:        "server-1",
	}
	domains := "apps.example.test\nother.example.test"
	https := true
	acmeEmail := "ops@example.test"
	marquee := domain.Marquee{
		ID:           7,
		Name:         "local",
		Host:         "127.0.0.1",
		User:         "root",
		Port:         22,
		DomainsInput: &domains,
		HTTPSEnabled: &https,
		AcmeEmail:    &acmeEmail,
	}

	err := checker.DeployCompose(context.Background(), marquee, "demo--42", 42, "services:\n  web:\n    image: nginx\n")
	if err != nil {
		t.Fatalf("deploy compose: %v", err)
	}

	seen := strings.Join(fake.Seen, "\n")
	for _, want := range []string{
		"mkdir -p '/opt/fibe/traefik/docker-config'",
		"read:/opt/fibe/traefik/acme.json",
		"write:/opt/fibe/traefik/acme.json:",
		"write:/opt/fibe/traefik/docker-config/config.json:",
		base64.StdEncoding.EncodeToString([]byte("dock-user:dock-token")),
		"traefik:ensure:--api=false",
		"--entrypoints.websecure.address=:443",
		"--providers.docker.constraints=Label(`fibe-distilled.managed`,`true`)",
		"--certificatesresolvers.letsencrypt.acme.email=ops@example.test",
		"--certificatesresolvers.letsencrypt.acme.httpchallenge=true",
		"DOCKER_CONFIG='/opt/fibe/playgrounds/demo--42/docker-config' docker compose -f compose.yml -p 'demo--42' up -d --remove-orphans --pull missing",
	} {
		if !strings.Contains(seen, want) {
			t.Fatalf("expected %q in runtime calls:\n%s", want, seen)
		}
	}
	assertRecordedShellCommandsParse(t, fake)
}

func TestDeployComposeRequiresAcmeEmailForRoutedMarquee(t *testing.T) {
	fake := &runtimetest.FakeExecutor{}
	checker := runtime.Checker{Executor: fake}
	domains := "apps.example.test"
	marquee := domain.Marquee{
		ID:           7,
		Name:         "local",
		Host:         "127.0.0.1",
		User:         "root",
		Port:         22,
		DomainsInput: &domains,
	}

	err := checker.DeployCompose(context.Background(), marquee, "demo--42", 42, "services:\n  web:\n    image: nginx\n")
	if err == nil || !strings.Contains(err.Error(), "requires acme_email") {
		t.Fatalf("expected ACME email error, got %v", err)
	}
}

func TestRuntimeArtifactDriftParsesRemoteDriftReport(t *testing.T) {
	project := "demo--42"
	base := optfibe.PlaygroundPath(project)
	fake := &runtimetest.FakeExecutor{
		ReadFiles: map[string]string{
			base + "/compose.yml":               "services:\n  web:\n    image: httpd\n",
			base + "/docker-config/config.json": "{}",
		},
	}
	checker := runtime.Checker{Executor: fake, InstanceID: "server-1"}
	marquee := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22}

	drift, err := checker.RuntimeArtifactDrift(context.Background(), marquee, project, "services:\n  web:\n    image: nginx\n")
	if err != nil {
		t.Fatalf("runtime artifact drift: %v", err)
	}
	if drift["compose"] != "drift" {
		t.Fatalf("unexpected drift report: %#v", drift)
	}
	if _, ok := drift["docker_config"]; ok {
		t.Fatalf("matching docker config should not be reported as drift: %#v", drift)
	}
	seen := strings.Join(fake.Seen, "\n")
	for _, want := range []string{
		"read:" + base + "/compose.yml",
		"read:" + base + "/docker-config/config.json",
	} {
		if !strings.Contains(seen, want) {
			t.Fatalf("expected %q in drift command:\n%s", want, seen)
		}
	}
}

func TestSourceDirtyPathsFailsClosedOnBrokenCheckoutEvidence(t *testing.T) {
	cleanPath := "/opt/fibe/playgrounds/demo--42/props/acme-clean/main"
	brokenPath := "/opt/fibe/playgrounds/demo--42/props/acme-broken/main"
	stderrPath := "/opt/fibe/playgrounds/demo--42/props/acme-stderr/main"
	fake := &runtimetest.FakeExecutor{
		ReadFiles: map[string]string{},
		ResultContains: map[string]runtime.CommandResult{
			cleanPath:  {},
			brokenPath: {Stdout: "fibe_distilled_source_dirty=missing_git\n"},
			stderrPath: {Stderr: "fatal: bad git metadata\n"},
		},
	}
	checker := runtime.Checker{Executor: fake, InstanceID: "server-1"}

	paths, err := checker.SourceDirtyPaths(context.Background(), domain.Marquee{ID: 7}, "demo--42", []string{cleanPath, brokenPath, stderrPath})
	if err != nil {
		t.Fatalf("source dirty check: %v", err)
	}
	if strings.Join(paths, ",") != brokenPath+","+stderrPath {
		t.Fatalf("expected broken checkout evidence to be dirty, got paths=%#v", paths)
	}
	seen := strings.Join(fake.Seen, "\n")
	for _, want := range []string{
		"FIBE_DISTILLED_SOURCE_DIRTY demo--42 " + cleanPath,
		"FIBE_DISTILLED_SOURCE_DIRTY demo--42 " + brokenPath,
		"FIBE_DISTILLED_SOURCE_DIRTY demo--42 " + stderrPath,
	} {
		if !strings.Contains(seen, want) {
			t.Fatalf("expected dirty check to contain %q:\n%s", want, seen)
		}
	}
}

func TestSourceDirtyPathsRejectsPathsOutsideProject(t *testing.T) {
	fake := &runtimetest.FakeExecutor{}
	checker := runtime.Checker{Executor: fake}
	_, err := checker.SourceDirtyPaths(context.Background(), domain.Marquee{}, "demo--42", []string{
		"/opt/fibe/playgrounds/other--1/props/acme-demo/main",
	})
	if err == nil || !strings.Contains(err.Error(), "source dirty path") {
		t.Fatalf("expected source path validation error, got %v", err)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("invalid source paths must not execute remote commands: %#v", fake.Seen)
	}
}

func TestBuildImagePinsSourceCommitAndBuildMetadata(t *testing.T) {
	sourcePath := "/opt/fibe/playgrounds/demo--42/props/acme-my-api/main"
	fake := &runtimetest.FakeExecutor{
		ReadFiles: map[string]string{},
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_RESOLVE_COMMIT": {Stdout: "abcdef123456\n"},
		},
	}
	checker := runtime.Checker{Executor: fake, InstanceID: "server-1"}
	marquee := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22}

	result, err := checker.BuildImage(context.Background(), marquee, runtime.BuildRequest{
		Project:             "demo--42",
		PlaygroundID:        42,
		ServiceName:         "web",
		ContextPath:         runtimetest.MustRemoteCheckoutPath(t, "demo--42", sourcePath),
		Dockerfile:          runtimetest.MustRelativeDockerfilePath(t, "Dockerfile.web"),
		Target:              "prod",
		Platform:            domain.BuildPlatform("linux/amd64"),
		BuildArgs:           []string{" RAILS_ENV = production "},
		BuildTime:           "2026-06-24T12:34:56Z",
		BuildIdentityDigest: "build-identity-digest",
		ImageRef:            "fibe-distilled/demo--42/web:abcdef123456",
	})
	if err != nil {
		t.Fatalf("build image: %v", err)
	}
	if result.CommitSHA != "abcdef123456" {
		t.Fatalf("unexpected commit: %#v", result)
	}
	seen := strings.Join(fake.Seen, "\n")
	for _, want := range []string{
		"FIBE_DISTILLED_RESOLVE_COMMIT demo--42 /opt/fibe/playgrounds/demo--42/props/acme-my-api/main",
		"FIBE_DISTILLED_BUILD_IMAGE DOCKER_CONFIG='/opt/fibe/playgrounds/demo--42/docker-config' docker build",
		"'-t' 'fibe-distilled/demo--42/web:abcdef123456'",
		"'-f' '/opt/fibe/playgrounds/demo--42/props/acme-my-api/main/Dockerfile.web'",
		"'--build-arg' 'RAILS_ENV=production'",
		"'--build-arg' 'FIBE_BUILD_TIME=2026-06-24T12:34:56Z'",
		"'--build-arg' 'FIBE_BUILD_GIT_COMMIT_SHA=abcdef123456'",
		"'--target' 'prod'",
		"'--platform' 'linux/amd64'",
		"'--label' 'fibe.build.git_commit_sha=abcdef123456'",
		"'--label' 'fibe.source_commit=abcdef123456'",
		"'--label' 'fibe.build.identity_digest=build-identity-digest'",
	} {
		if !strings.Contains(seen, want) {
			t.Fatalf("expected %q in build command:\n%s", want, seen)
		}
	}
}

func TestBuildImageRejectsMalformedBuildArgsBeforeRuntimeCommand(t *testing.T) {
	sourcePath := "/opt/fibe/playgrounds/demo--42/props/acme-my-api/main"
	fake := &runtimetest.FakeExecutor{}
	checker := runtime.Checker{Executor: fake, InstanceID: "server-1"}
	marquee := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22}

	_, err := checker.BuildImage(context.Background(), marquee, runtime.BuildRequest{
		Project:      "demo--42",
		PlaygroundID: 42,
		ServiceName:  "web",
		ContextPath:  runtimetest.MustRemoteCheckoutPath(t, "demo--42", sourcePath),
		Dockerfile:   runtimetest.MustRelativeDockerfilePath(t, "Dockerfile.web"),
		BuildArgs:    []string{"BAD-KEY=value"},
	})
	if err == nil || !strings.Contains(err.Error(), "build arg") {
		t.Fatalf("expected build arg validation error, got %v", err)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("malformed build args must not execute remote commands: %#v", fake.Seen)
	}
}

func TestBuildImageRejectsUnsupportedPlatformBeforeRuntimeCommand(t *testing.T) {
	sourcePath := "/opt/fibe/playgrounds/demo--42/props/acme-my-api/main"
	fake := &runtimetest.FakeExecutor{}
	checker := runtime.Checker{Executor: fake, InstanceID: "server-1"}
	marquee := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22}

	_, err := checker.BuildImage(context.Background(), marquee, runtime.BuildRequest{
		Project:      "demo--42",
		PlaygroundID: 42,
		ServiceName:  "web",
		ContextPath:  runtimetest.MustRemoteCheckoutPath(t, "demo--42", sourcePath),
		Dockerfile:   runtimetest.MustRelativeDockerfilePath(t, "Dockerfile.web"),
		Platform:     domain.BuildPlatform("linux/x86_64"),
	})
	if err == nil || !strings.Contains(err.Error(), "build platform") {
		t.Fatalf("expected build platform validation error, got %v", err)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("unsupported build platform must not execute remote commands: %#v", fake.Seen)
	}
}

func TestBuildImageRejectsMissingServiceNameBeforeRuntimeCommand(t *testing.T) {
	sourcePath := "/opt/fibe/playgrounds/demo--42/props/acme-my-api/main"
	fake := &runtimetest.FakeExecutor{}
	checker := runtime.Checker{Executor: fake, InstanceID: "server-1"}

	_, err := checker.BuildImage(context.Background(), domain.Marquee{}, runtime.BuildRequest{
		Project:      "demo--42",
		PlaygroundID: 42,
		ServiceName:  " ",
		ContextPath:  runtimetest.MustRemoteCheckoutPath(t, "demo--42", sourcePath),
		Dockerfile:   runtimetest.MustRelativeDockerfilePath(t, "Dockerfile.web"),
		CommitSHA:    "abcdef123456",
		ImageRef:     "fibe-distilled/demo--42/custom:abcdef123456",
	})
	if err == nil || !strings.Contains(err.Error(), "service name") {
		t.Fatalf("expected service name validation error, got %v", err)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("missing service name must not execute remote commands: %#v", fake.Seen)
	}
}

func TestBuildPathConstructorsRejectUnsafePaths(t *testing.T) {
	validSourcePath := "/opt/fibe/playgrounds/demo--42/props/acme-my-api/main"
	cases := []struct {
		name       string
		sourcePath string
		dockerfile string
		want       string
	}{
		{name: "blank source", sourcePath: "", dockerfile: "Dockerfile", want: "runtime checkout path"},
		{name: "outside opt fibe", sourcePath: "/src", dockerfile: "Dockerfile", want: "runtime checkout path"},
		{name: "project root source", sourcePath: "/opt/fibe/playgrounds/demo--42", dockerfile: "Dockerfile", want: "runtime checkout path"},
		{name: "cross project source", sourcePath: "/opt/fibe/playgrounds/other--99/props/acme-my-api/main", dockerfile: "Dockerfile", want: "runtime checkout path"},
		{name: "source parent traversal", sourcePath: "/opt/fibe/playgrounds/demo--42/../other", dockerfile: "Dockerfile", want: "runtime checkout path"},
		{name: "absolute dockerfile", sourcePath: validSourcePath, dockerfile: "/Dockerfile", want: "dockerfile path"},
		{name: "leading dockerfile traversal", sourcePath: validSourcePath, dockerfile: "../Dockerfile", want: "dockerfile path"},
		{name: "nested dockerfile traversal", sourcePath: validSourcePath, dockerfile: "deploy/../Dockerfile", want: "dockerfile path"},
		{name: "nul dockerfile", sourcePath: validSourcePath, dockerfile: "Dockerfile\x00", want: "dockerfile path"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, contextErr := runtime.NewRemoteCheckoutPath("demo--42", tt.sourcePath)
			_, dockerfileErr := runtime.NewRelativeDockerfilePath(tt.dockerfile)
			err := contextErr
			if err == nil {
				err = dockerfileErr
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestRemoteCheckoutPathDerivesRepositoryParent(t *testing.T) {
	value, err := runtime.NewRemoteCheckoutPath("demo--42", "/opt/fibe/playgrounds/demo--42/props/acme-my-api/main")
	if err != nil {
		t.Fatalf("remote checkout path: %v", err)
	}
	if value.String() != "/opt/fibe/playgrounds/demo--42/props/acme-my-api/main" {
		t.Fatalf("unexpected checkout path: %s", value.String())
	}
	if value.Parent() != "/opt/fibe/playgrounds/demo--42/props/acme-my-api" {
		t.Fatalf("unexpected source checkout parent: %s", value.Parent())
	}
}

func TestBuildImageRejectsMissingBuildContextBeforeRuntimeCommands(t *testing.T) {
	fake := &runtimetest.FakeExecutor{}
	checker := runtime.Checker{Executor: fake}
	_, err := checker.BuildImage(context.Background(), domain.Marquee{}, runtime.BuildRequest{
		Project:     "demo--42",
		CommitSHA:   "abcdef123456",
		ServiceName: "web",
	})
	if err == nil || !strings.Contains(err.Error(), "runtime checkout path") {
		t.Fatalf("expected runtime checkout error, got %v", err)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("invalid build request must not execute runtime commands: %#v", fake.Seen)
	}
}

func TestImageExistsForBuildUsesFibeBuildMetadata(t *testing.T) {
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"docker image inspect 'fibe-distilled/demo/web:abcdef'": {
				Stdout: `{"Labels":{"fibe.build.git_commit_sha":"abcdef"},"Env":["FIBE_BUILD_GIT_COMMIT_SHA=ignored"]}`,
			},
			"docker image inspect 'fibe-distilled/demo/web:env-only'": {
				Stdout: `{"Labels":{},"Env":["FIBE_BUILD_GIT_COMMIT_SHA=abcdef"]}`,
			},
		},
	}
	checker := runtime.Checker{Executor: fake}

	exists, err := checker.ImageExistsForBuild(context.Background(), domain.Marquee{}, "fibe-distilled/demo/web:abcdef", "abcdef", "")
	if err != nil {
		t.Fatalf("image exists for commit: %v", err)
	}
	if !exists {
		t.Fatalf("expected matching image metadata")
	}
	exists, err = checker.ImageExistsForBuild(context.Background(), domain.Marquee{}, "fibe-distilled/demo/web:abcdef", "abcdef", "identity")
	if err != nil {
		t.Fatalf("image exists for build identity: %v", err)
	}
	if exists {
		t.Fatalf("expected mismatched build identity metadata to fail")
	}
	exists, err = checker.ImageExistsForBuild(context.Background(), domain.Marquee{}, "fibe-distilled/demo/web:env-only", "abcdef", "")
	if err != nil {
		t.Fatalf("image exists for env-only metadata: %v", err)
	}
	if exists {
		t.Fatalf("env-only build metadata should not satisfy fibe-distilled image reuse")
	}
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "docker image inspect 'fibe-distilled/demo/web:abcdef' --format '{{json .Config}}'") {
		t.Fatalf("expected docker image inspect command:\n%s", seen)
	}
}

func TestImageExistsForBuildTreatsMissingImageAsAbsent(t *testing.T) {
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{
			"docker image inspect": errors.New("exit status 1"),
		},
		ResultContains: map[string]runtime.CommandResult{
			"docker image inspect": {Stderr: "Error response from daemon: No such image: fibe-distilled/demo/web:missing"},
		},
	}
	checker := runtime.Checker{Executor: fake}

	exists, err := checker.ImageExistsForBuild(context.Background(), domain.Marquee{}, "fibe-distilled/demo/web:missing", "abcdef", "")
	if err != nil {
		t.Fatalf("missing image should not be an infrastructure error: %v", err)
	}
	if exists {
		t.Fatal("missing image should not exist")
	}
}

func TestImageExistsForBuildSkipsInvalidCommitEvidence(t *testing.T) {
	fake := &runtimetest.FakeExecutor{}
	checker := runtime.Checker{Executor: fake}

	exists, err := checker.ImageExistsForBuild(context.Background(), domain.Marquee{}, "fibe-distilled/demo/web:bad", "abc def", "")
	if err != nil {
		t.Fatalf("invalid commit evidence should not be an error: %v", err)
	}
	if exists {
		t.Fatal("invalid commit evidence should not report image existence")
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("invalid commit evidence should not inspect remote images: %#v", fake.Seen)
	}
}

func TestImageExistsForBuildReturnsInspectInfrastructureErrors(t *testing.T) {
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{
			"docker image inspect": errors.New("docker failed"),
		},
		ResultContains: map[string]runtime.CommandResult{
			"docker image inspect": {Stderr: "permission denied"},
		},
	}
	checker := runtime.Checker{Executor: fake}

	exists, err := checker.ImageExistsForBuild(context.Background(), domain.Marquee{}, "fibe-distilled/demo/web:abcdef", "abcdef", "")
	if err == nil || !strings.Contains(err.Error(), "docker image inspect failed") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected inspect infrastructure error, got exists=%v err=%v", exists, err)
	}
	if exists {
		t.Fatal("failed inspect should not report image existence")
	}
}

func TestImageExistsForBuildReturnsMalformedInspectOutputErrors(t *testing.T) {
	for _, stdout := range []string{"not-json", "null"} {
		t.Run(stdout, func(t *testing.T) {
			fake := &runtimetest.FakeExecutor{
				ResultContains: map[string]runtime.CommandResult{
					"docker image inspect": {Stdout: stdout},
				},
			}
			checker := runtime.Checker{Executor: fake}

			exists, err := checker.ImageExistsForBuild(context.Background(), domain.Marquee{}, "fibe-distilled/demo/web:abcdef", "abcdef", "")
			if err == nil || !strings.Contains(err.Error(), "parse image config JSON") {
				t.Fatalf("expected malformed inspect JSON error, got exists=%v err=%v", exists, err)
			}
			if exists {
				t.Fatal("malformed inspect output should not report image existence")
			}
		})
	}
}

func TestBuildImageFailsWhenCommitCannotBeResolved(t *testing.T) {
	sourcePath := "/opt/fibe/playgrounds/demo--42/props/acme-my-api/main"
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_RESOLVE_COMMIT": {Stderr: "fatal: not a git repository"},
		},
		ErrorContains: map[string]error{
			"FIBE_DISTILLED_RESOLVE_COMMIT": errors.New("exit status 128"),
		},
	}
	checker := runtime.Checker{Executor: fake}
	_, err := checker.BuildImage(context.Background(), domain.Marquee{}, runtime.BuildRequest{
		Project:     "demo--42",
		ContextPath: runtimetest.MustRemoteCheckoutPath(t, "demo--42", sourcePath),
		ServiceName: "web",
		ImageRef:    "image",
	})
	if err == nil || !strings.Contains(err.Error(), "resolve source commit failed") {
		t.Fatalf("expected commit failure, got %v", err)
	}
}

func TestBuildImageRejectsInvalidImageRefBeforeRemoteCommands(t *testing.T) {
	sourcePath := "/opt/fibe/playgrounds/demo--42/props/acme-my-api/main"
	for _, imageRef := range []string{
		"BadUpper:latest",
		"fibe-distilled/demo/web bad:latest",
		"fibe-distilled/demo/web@sha256:" + strings.Repeat("0", 64),
	} {
		t.Run(imageRef, func(t *testing.T) {
			fake := &runtimetest.FakeExecutor{}
			checker := runtime.Checker{Executor: fake}
			_, err := checker.BuildImage(context.Background(), domain.Marquee{}, runtime.BuildRequest{
				Project:     "demo--42",
				CommitSHA:   "abcdef123456",
				ContextPath: runtimetest.MustRemoteCheckoutPath(t, "demo--42", sourcePath),
				ServiceName: "web",
				ImageRef:    imageRef,
			})
			if err == nil || !strings.Contains(err.Error(), "image ref") {
				t.Fatalf("expected image ref validation error, got %v", err)
			}
			if len(fake.Seen) != 0 {
				t.Fatalf("invalid image ref must not execute remote commands: %#v", fake.Seen)
			}
		})
	}
}

func TestBuildImageRejectsInvalidCommitEvidenceBeforeBuild(t *testing.T) {
	sourcePath := "/opt/fibe/playgrounds/demo--42/props/acme-my-api/main"
	marquee := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22}

	t.Run("supplied commit", func(t *testing.T) {
		fake := &runtimetest.FakeExecutor{}
		checker := runtime.Checker{Executor: fake}
		_, err := checker.BuildImage(context.Background(), marquee, runtime.BuildRequest{
			Project:     "demo--42",
			CommitSHA:   "abc def",
			ContextPath: runtimetest.MustRemoteCheckoutPath(t, "demo--42", sourcePath),
			ServiceName: "web",
			ImageRef:    "fibe-distilled/demo--42/web:bad",
		})
		if err == nil || !strings.Contains(err.Error(), "build commit") {
			t.Fatalf("expected supplied commit validation error, got %v", err)
		}
		if len(fake.Seen) != 0 {
			t.Fatalf("invalid supplied commit must not execute remote commands: %#v", fake.Seen)
		}
	})

	t.Run("resolved commit", func(t *testing.T) {
		fake := &runtimetest.FakeExecutor{
			ResultContains: map[string]runtime.CommandResult{
				"FIBE_DISTILLED_RESOLVE_COMMIT": {Stdout: "abcdef\n123456\n"},
			},
		}
		checker := runtime.Checker{Executor: fake}
		_, err := checker.BuildImage(context.Background(), marquee, runtime.BuildRequest{
			Project:     "demo--42",
			ContextPath: runtimetest.MustRemoteCheckoutPath(t, "demo--42", sourcePath),
			ServiceName: "web",
			ImageRef:    "fibe-distilled/demo--42/web:bad",
		})
		if err == nil || !strings.Contains(err.Error(), "invalid HEAD") {
			t.Fatalf("expected resolved commit validation error, got %v", err)
		}
		if seen := strings.Join(fake.Seen, "\n"); strings.Contains(seen, "/opt/fibe/builds/remote_build.sh") {
			t.Fatalf("invalid resolved commit must not run build wrapper:\n%s", seen)
		}
	})
}

func TestDestroyComposeRunsStrictDownAndRemovesSafeProjectRoot(t *testing.T) {
	fake := &runtimetest.FakeExecutor{
		ReadFiles: map[string]string{
			optfibe.PlaygroundPath("demo--42") + "/compose.yml": "services:\n  web:\n    image: nginx\n",
		},
	}
	checker := runtime.Checker{Executor: fake, InstanceID: "server-1"}
	marquee := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22}

	err := checker.DestroyCompose(context.Background(), marquee, "demo--42")
	if err != nil {
		t.Fatalf("destroy compose: %v", err)
	}

	seen := strings.Join(fake.Seen, "\n")
	for _, want := range []string{
		"read:/opt/fibe/playgrounds/demo--42/compose.yml",
		"cd '/opt/fibe/playgrounds/demo--42' && docker compose -f compose.yml -p 'demo--42' down --remove-orphans -v",
		"docker:cleanup:demo--42:volumes=true",
		"remove:/opt/fibe/playgrounds/demo--42",
	} {
		if !strings.Contains(seen, want) {
			t.Fatalf("expected %q in destroy command:\n%s", want, seen)
		}
	}
	if strings.Contains(seen, "down --remove-orphans -v || true") {
		t.Fatalf("destroy must not swallow compose down failures:\n%s", seen)
	}
	assertRecordedShellCommandsParse(t, fake)
}

func TestDownComposeKeepsRuntimeFilesForRestart(t *testing.T) {
	fake := &runtimetest.FakeExecutor{
		ReadFiles: map[string]string{
			optfibe.PlaygroundPath("demo--42") + "/compose.yml": "services:\n  web:\n    image: nginx\n",
		},
	}
	checker := runtime.Checker{Executor: fake, InstanceID: "server-1"}

	err := checker.DownCompose(context.Background(), domain.Marquee{ID: 7}, "demo--42")
	if err != nil {
		t.Fatalf("down compose: %v", err)
	}

	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "cd '/opt/fibe/playgrounds/demo--42' && docker compose -f compose.yml -p 'demo--42' down --remove-orphans") {
		t.Fatalf("expected compose down command:\n%s", seen)
	}
	for _, want := range []string{
		"read:/opt/fibe/playgrounds/demo--42/compose.yml",
		"docker:cleanup:demo--42:volumes=false",
	} {
		if !strings.Contains(seen, want) {
			t.Fatalf("expected guard %q in command:\n%s", want, seen)
		}
	}
	if strings.Contains(seen, "rm -rf") || strings.Contains(seen, "docker volume rm") {
		t.Fatalf("restart down must not remove files or volumes:\n%s", seen)
	}
	assertRecordedShellCommandsParse(t, fake)
}

func TestLifecycleComposeRejectsUnsafeProjectNames(t *testing.T) {
	for _, action := range lifecycleComposeActions() {
		for _, project := range []string{"../demo", "demo name", "Demo", "_demo", "-demo", "demo.name"} {
			t.Run(action.name+"/"+project, func(t *testing.T) {
				assertUnsafeProjectRejected(t, action.run, project)
			})
		}
	}
}

func TestLifecycleComposeRejectsMissingProjectNames(t *testing.T) {
	for _, action := range lifecycleComposeActions() {
		t.Run(action.name, func(t *testing.T) {
			assertMissingProjectRejected(t, action.run, " ")
		})
	}
}

type runtimeProjectAction struct {
	name string
	run  func(runtime.Checker, domain.Marquee, string) error
}

func lifecycleComposeActions() []runtimeProjectAction {
	return []runtimeProjectAction{
		{name: "destroy", run: func(c runtime.Checker, m domain.Marquee, p string) error {
			return c.DestroyCompose(context.Background(), m, p)
		}},
		{name: "start", run: func(c runtime.Checker, m domain.Marquee, p string) error {
			return c.StartCompose(context.Background(), m, p)
		}},
		{name: "stop", run: func(c runtime.Checker, m domain.Marquee, p string) error {
			return c.StopCompose(context.Background(), m, p)
		}},
	}
}

func projectScopedRuntimeActions() []runtimeProjectAction {
	return []runtimeProjectAction{
		{name: "deploy", run: func(c runtime.Checker, m domain.Marquee, p string) error {
			return c.DeployCompose(context.Background(), m, p, 42, "services:\n  web:\n    image: nginx\n")
		}},
		{name: "build", run: func(c runtime.Checker, m domain.Marquee, p string) error {
			_, err := c.BuildImage(context.Background(), m, runtime.BuildRequest{Project: p, CommitSHA: "abcdef", ServiceName: "web"})
			return err
		}},
		{name: "inspect", run: func(c runtime.Checker, m domain.Marquee, p string) error {
			_, err := c.InspectServices(context.Background(), m, p)
			return err
		}},
		{name: "drift", run: func(c runtime.Checker, m domain.Marquee, p string) error {
			_, err := c.RuntimeArtifactDrift(context.Background(), m, p, "services:\n  web:\n    image: nginx\n")
			return err
		}},
		{name: "logs", run: func(c runtime.Checker, m domain.Marquee, p string) error {
			_, err := c.Logs(context.Background(), m, p, "web", 10)
			return err
		}},
	}
}

func assertUnsafeProjectRejected(t *testing.T, run func(runtime.Checker, domain.Marquee, string) error, project string) {
	t.Helper()
	assertProjectRejectedBeforeRemoteCommand(t, run, project, "unsafe compose project", "unsafe project")
}

func assertMissingProjectRejected(t *testing.T, run func(runtime.Checker, domain.Marquee, string) error, project string) {
	t.Helper()
	assertProjectRejectedBeforeRemoteCommand(t, run, project, "compose project is required", "missing project")
}

func assertProjectRejectedBeforeRemoteCommand(t *testing.T, run func(runtime.Checker, domain.Marquee, string) error, project string, expected string, label string) {
	t.Helper()
	fake := &runtimetest.FakeExecutor{}
	checker := runtime.Checker{Executor: fake}
	err := run(checker, domain.Marquee{}, project)
	if err == nil || !strings.Contains(err.Error(), expected) {
		t.Fatalf("expected %s error containing %q, got %v", label, expected, err)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("%s must not execute remote commands: %#v", label, fake.Seen)
	}
}

func TestProjectScopedRuntimeCommandsRejectUnsafeProjectNames(t *testing.T) {
	for _, action := range projectScopedRuntimeActions() {
		t.Run(action.name, func(t *testing.T) {
			assertUnsafeProjectRejected(t, action.run, "../demo")
		})
	}
}

func TestProjectScopedRuntimeCommandsRejectMissingProjectNames(t *testing.T) {
	for _, action := range projectScopedRuntimeActions() {
		t.Run(action.name, func(t *testing.T) {
			assertMissingProjectRejected(t, action.run, " ")
		})
	}
}

func assertRecordedShellCommandsParse(t *testing.T, fake *runtimetest.FakeExecutor) {
	t.Helper()
	for _, command := range fake.Seen {
		if strings.HasPrefix(command, "write:") ||
			strings.HasPrefix(command, "read:") ||
			strings.HasPrefix(command, "traefik:ensure:") ||
			strings.HasPrefix(command, "docker:cleanup:") ||
			strings.HasPrefix(command, "docker:ping") ||
			strings.HasPrefix(command, "remove:") {
			continue
		}
		result := exec.Command("sh", "-n", "-c", command)
		output, err := result.CombinedOutput()
		if err != nil {
			t.Fatalf("recorded shell command does not parse: %v\n%s\ncommand:\n%s", err, output, command)
		}
	}
}

func TestDestroyComposePropagatesRemoteFailure(t *testing.T) {
	fake := &runtimetest.FakeExecutor{
		ReadFiles: map[string]string{
			optfibe.PlaygroundPath("demo--42") + "/compose.yml": "services:\n  web:\n    image: nginx\n",
		},
		ErrorContains: map[string]error{
			"down --remove-orphans -v": errors.New("exit status 1"),
		},
		ResultContains: map[string]runtime.CommandResult{
			"down --remove-orphans -v": {Stderr: "compose down failed"},
		},
	}
	checker := runtime.Checker{Executor: fake, InstanceID: "server-1"}

	err := checker.DestroyCompose(context.Background(), domain.Marquee{ID: 7}, "demo--42")
	if err == nil || !strings.Contains(err.Error(), "compose down failed") {
		t.Fatalf("expected remote failure, got %v", err)
	}
}

func TestLogsTailZeroRequestsAllLogs(t *testing.T) {
	project := "logs--1"
	fake := &runtimetest.FakeExecutor{
		ReadFiles: map[string]string{
			optfibe.PlaygroundPath(project) + "/compose.yml": "services:\n  web:\n    image: nginx\n",
		},
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_LOGS": {Stdout: "one\ntwo\n"},
		},
	}
	checker := runtime.Checker{Executor: fake, InstanceID: "server-1"}
	lines, err := checker.Logs(context.Background(), domain.Marquee{ID: 7}, project, "web", 0)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Fatalf("unexpected lines: %#v", lines)
	}
	seen := strings.Join(fake.Seen, "\n")
	for _, want := range []string{
		"read:/opt/fibe/playgrounds/logs--1/compose.yml",
		"FIBE_DISTILLED_LOGS logs--1 /opt/fibe/playgrounds/logs--1 web all",
	} {
		if !strings.Contains(seen, want) {
			t.Fatalf("expected logs command to contain %q:\n%s", want, seen)
		}
	}
}

func TestInspectServicesUsesDockerComposeState(t *testing.T) {
	project := "demo--1"
	fake := &runtimetest.FakeExecutor{
		ReadFiles: map[string]string{
			optfibe.PlaygroundPath(project) + "/compose.yml": "services:\n  web:\n    image: nginx\n",
		},
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_INSPECT": {Stdout: `[{"Service":"web","State":"running","Health":"healthy","ExitCode":0},{"Service":"producer","State":"running","Health":"","ExitCode":0},{"Service":"worker","State":"exited","Health":"","ExitCode":1},{"Service":"helper","State":"created","ExitCode":0},{"Service":"   ","Name":" fallback ","State":" running ","Health":" healthy ","ExitCode":0}]`},
		},
	}
	checker := runtime.Checker{Executor: fake, InstanceID: "server-1"}
	services, err := checker.InspectServices(context.Background(), domain.Marquee{ID: 7}, project)
	if err != nil {
		t.Fatalf("inspect services: %v", err)
	}
	if len(services) != 5 {
		t.Fatalf("expected services, got %#v", services)
	}
	assertComposeService(t, services[0], "web", "running", "healthy", true, nil)
	assertComposeService(t, services[1], "producer", "running", "", true, nil)
	assertComposeService(t, services[2], "worker", "exited", "", false, ptrInt(1))
	assertComposeService(t, services[3], "helper", "created", "", false, nil)
	assertComposeService(t, services[4], "fallback", "running", "healthy", true, nil)
	seen := strings.Join(fake.Seen, "\n")
	for _, want := range []string{
		"read:/opt/fibe/playgrounds/demo--1/compose.yml",
		"FIBE_DISTILLED_INSPECT demo--1 /opt/fibe/playgrounds/demo--1",
	} {
		if !strings.Contains(seen, want) {
			t.Fatalf("expected inspect command to contain %q:\n%s", want, seen)
		}
	}
}

func assertComposeService(t *testing.T, service domain.PlaygroundServiceInfo, name string, status string, health string, running bool, exitCode *int) {
	t.Helper()
	if service.Name != name || service.Status != status || service.Health != health || service.Running != running {
		t.Fatalf("unexpected compose service state: got %#v", service)
	}
	if exitCode == nil && service.ExitCode != nil {
		t.Fatalf("expected nil exit code for %s, got %#v", name, service.ExitCode)
	}
	if exitCode != nil && (service.ExitCode == nil || *service.ExitCode != *exitCode) {
		t.Fatalf("expected exit code %d for %s, got %#v", *exitCode, name, service.ExitCode)
	}
}

func ptrInt(value int) *int {
	return &value
}

func TestInspectServicesFailsOnMalformedComposeState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		stdout    string
		wantError string
	}{
		{name: "truncated array", stdout: `[{"Service":"web","State":"running"`, wantError: "parse docker compose ps JSON array"},
		{name: "top-level null", stdout: `null`, wantError: "expected object"},
		{name: "null array item", stdout: `[null]`, wantError: "expected object"},
		{name: "empty object", stdout: `{}`, wantError: "missing service name"},
		{name: "array empty object", stdout: `[{}]`, wantError: "missing service name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := "demo--malformed"
			fake := &runtimetest.FakeExecutor{
				ReadFiles: map[string]string{
					optfibe.PlaygroundPath(project) + "/compose.yml": "services:\n  web:\n    image: nginx\n",
				},
				ResultContains: map[string]runtime.CommandResult{
					"FIBE_DISTILLED_INSPECT": {Stdout: tc.stdout},
				},
			}
			checker := runtime.Checker{Executor: fake}

			_, err := checker.InspectServices(context.Background(), domain.Marquee{}, project)
			if err == nil || !strings.Contains(err.Error(), "inspect compose services failed") || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("expected malformed compose state error containing %q, got %v", tc.wantError, err)
			}
		})
	}
}
