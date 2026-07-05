package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/config"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	store "github.com/fibegg/fibe-distilled/internal/storage"
	"github.com/fibegg/fibe-distilled/internal/worker"
)

func TestGitHubWebhookRequiresConfiguredSecretAndValidSignature(t *testing.T) {
	body := `{"zen":"ok"}`
	blankSecretServer, _ := newGitHubWebhookTestServer(t, "", nil)
	res := doGitHubWebhook(t, blankSecretServer, "push", body, githubWebhookTestSignature("secret", body))
	assertWebhookStatus(t, res, http.StatusUnauthorized)

	signedServer, _ := newGitHubWebhookTestServer(t, "secret", nil)
	res = doGitHubWebhook(t, signedServer, "push", body, "sha256=bad")
	assertWebhookStatus(t, res, http.StatusUnauthorized)
}

func TestGitHubWebhookRejectsInvalidJSONAfterSignature(t *testing.T) {
	srv, _ := newGitHubWebhookTestServer(t, "secret", nil)
	body := `{"ref":`
	res := doGitHubWebhook(t, srv, "push", body, githubWebhookTestSignature("secret", body))
	assertWebhookStatus(t, res, http.StatusBadRequest)
}

func TestGitHubWebhookNoopsForNonActionableDeliveries(t *testing.T) {
	fake := &runtimetest.FakeExecutor{}
	srv, _ := newGitHubWebhookTestServer(t, "secret", fake)
	cases := []struct {
		name  string
		event string
		body  string
	}{
		{name: "ping", event: "ping", body: `{"zen":"ok"}`},
		{name: "tag", event: "push", body: `{"ref":"refs/tags/v1.0.0","after":"abcdef1234567890","repository":{"full_name":"acme/demo"}}`},
		{name: "deleted branch", event: "push", body: `{"ref":"refs/heads/main","after":"0000000000000000000000000000000000000000","repository":{"full_name":"acme/demo"}}`},
		{name: "missing repo", event: "push", body: `{"ref":"refs/heads/main","after":"abcdef1234567890","repository":{}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := doGitHubWebhook(t, srv, tc.event, tc.body, githubWebhookTestSignature("secret", tc.body))
			assertWebhookStatus(t, res, http.StatusOK)
		})
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("non-actionable webhook deliveries should not run worker commands:\n%s", strings.Join(fake.Seen, "\n"))
	}
}

func TestGitHubWebhookStartsWorkerWithoutBearerToken(t *testing.T) {
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_RESOLVE_COMMIT": {Stdout: "abcdef1234567890\n"},
		},
	}
	srv, st := newGitHubWebhookTestServer(t, "secret", fake)
	ctx := context.Background()
	mq := ensureTestConfiguredMarquee(t, st)
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name: "webhook-build-spec",
		BaseComposeYAML: `services:
  web:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
      fibe.gg/production: "true"
`,
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	project := "webhook-build--1"
	if _, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "webhook-build-pg",
		Status:         domain.StatusRunning,
		PlayspecID:     ps.ID,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	}); err != nil {
		t.Fatalf("create playground: %v", err)
	}
	body := `{"ref":"refs/heads/main","after":"abcdef1234567890","repository":{"full_name":"acme/demo"}}`
	res := doGitHubWebhook(t, srv, "push", body, githubWebhookTestSignature("secret", body))
	assertWebhookStatus(t, res, http.StatusOK)
	waitForWebhookWorker(t, st, fake)
}

func TestServerInfoReportsGitHubWebhookFeature(t *testing.T) {
	srv, _ := newGitHubWebhookTestServer(t, "secret", nil)
	var body map[string]any
	res := doReq(t, srv, http.MethodGet, "/api/server-info", nil, "test-token")
	decodeResp(t, res, &body)
	features := body["features"].(map[string]any)
	if features["github_push_webhooks"] != true {
		t.Fatalf("server-info should expose configured webhook feature, got %#v", body)
	}
}

func newGitHubWebhookTestServer(t *testing.T, secret string, fake *runtimetest.FakeExecutor) (*httptest.Server, *store.DB) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ensureTestConfiguredMarquee(t, st)
	if fake == nil {
		fake = &runtimetest.FakeExecutor{}
	}
	ensureDefaultComposePSResult(fake)
	app := New(config.Config{
		APIToken:            "test-token",
		GitHubTok:           "github-token",
		GitHubWebhookSecret: secret,
	}, st, worker.Worker{
		DB: st,
		Runtime: runtime.Checker{
			Executor: fake,
		},
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv, st
}

func doGitHubWebhook(t *testing.T, srv *httptest.Server, event string, body string, signature string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/webhooks/github", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-Hub-Signature-256", signature)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return res
}

func githubWebhookTestSignature(secret string, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func assertWebhookStatus(t *testing.T, res *http.Response, status int) {
	t.Helper()
	defer closeResponseBody(t, res)
	if res.StatusCode != status {
		t.Fatalf("webhook status = %d, want %d", res.StatusCode, status)
	}
}

func waitForWebhookWorker(t *testing.T, st *store.DB, fake *runtimetest.FakeExecutor) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pg, err := st.GetPlayground(context.Background(), "webhook-build-pg")
		if err == nil && pg.Status == domain.StatusHasChanges && pg.StateReason != nil && *pg.StateReason == "webhook_build_ready" {
			if !strings.Contains(strings.Join(fake.Seen, "\n"), "FIBE_DISTILLED_BUILD_IMAGE") {
				t.Fatalf("webhook worker marked ready without running build:\n%s", strings.Join(fake.Seen, "\n"))
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("webhook worker did not finish build path:\n%s", strings.Join(fake.Seen, "\n"))
}
