package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/config"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	store "github.com/fibegg/fibe-distilled/internal/storage"
	"github.com/fibegg/fibe-distilled/internal/worker"
)

func TestMarqueeManagementAPIsUnsupported(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	for _, req := range []struct {
		method string
		path   string
		body   any
	}{
		{method: http.MethodPost, path: "/api/marquees", body: map[string]any{"marquee": map[string]any{"name": "host"}}},
		{method: http.MethodPost, path: "/api/marquees", body: map[string]any{"marquee": map[string]any{
			"name":                   "host",
			"domains_input":          "apps.example.test",
			"https_enabled":          false,
			"tls_certificate_source": "provided",
			"tls_certificate_pem":    "cert",
			"tls_private_key_pem":    "key",
			"dockerhub_auth_enabled": true,
			"dockerhub_username":     "u",
			"dockerhub_token":        "t",
			"dns_provider":           "cloudflare",
			"dns_credentials":        map[string]any{"token": "secret"},
			"build_platform":         "linux/amd64",
			"prop_id":                1,
		}}},
		{method: http.MethodPatch, path: "/api/marquees/default", body: map[string]any{"marquee": map[string]any{"name": "renamed"}}},
		{method: http.MethodDelete, path: "/api/marquees/default"},
		{method: http.MethodPost, path: "/api/marquees/default/connection_tests"},
		{method: http.MethodPost, path: "/api/marquees/default/ssh_keys"},
		{method: http.MethodPost, path: "/api/marquees/default/certificates"},
		{method: http.MethodPost, path: "/api/marquees/default/dns_records"},
		{method: http.MethodPatch, path: "/api/marquees/default/docker_credentials", body: map[string]any{"dockerhub_username": "u"}},
		{method: http.MethodGet, path: "/api/marquees/default/status"},
		{method: http.MethodPost, path: "/api/autoconnect_tokens", body: map[string]any{"domain": "apps.example.test", "ssl_mode": "letsencrypt"}},
	} {
		res := doReq(t, srv, req.method, req.path, req.body, "test-token")
		assertErrorCode(t, res, http.StatusNotImplemented, "NOT_IMPLEMENTED")
	}
}

func TestConfiguredMarqueeReadOnlyDiscovery(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fibe-distilled.sqlite3")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	domainName := "configured.example.test"
	https := true
	acme := "ops@example.test"
	cfg := config.Config{
		APIToken:  "test-token",
		GitHubTok: "github-token",
	}
	mq, err := st.EnsureConfiguredMarquee(ctx, domain.Marquee{
		Name:          store.ConfiguredMarqueeName,
		Host:          "localhost",
		Port:          0,
		User:          "local",
		DomainsInput:  &domainName,
		HTTPSEnabled:  &https,
		AcmeEmail:     &acme,
		SSHPrivateKey: "",
		Status:        "active",
	})
	if err != nil {
		t.Fatalf("ensure configured marquee: %v", err)
	}
	legacyDomain := "legacy.example.test"
	legacyHTTPS := true
	legacy := runtimetest.InsertLegacyMarquee(ctx, t, dbPath, domain.Marquee{
		Name:          "legacy-host",
		Host:          "127.0.0.2",
		Port:          22,
		User:          "root",
		DomainsInput:  &legacyDomain,
		HTTPSEnabled:  &legacyHTTPS,
		AcmeEmail:     &acme,
		SSHPrivateKey: "test",
		Status:        "active",
	})
	srv := httptest.NewServer(New(cfg, st, worker.Worker{DB: st}))
	defer srv.Close()

	var list map[string]any
	res := doReq(t, srv, http.MethodGet, "/api/marquees", nil, "test-token")
	decodeResp(t, res, &list)
	data := list["data"].([]any)
	if len(data) != 1 || numberID(data[0].(map[string]any)["id"]) != idString(mq.ID) {
		t.Fatalf("unexpected marquee list: %#v", list)
	}

	var shown map[string]any
	res = doReq(t, srv, http.MethodGet, "/api/marquees/default", nil, "test-token")
	decodeResp(t, res, &shown)
	if shown["domains_input"] != domainName || shown["https_enabled"] != true ||
		shown["host"] != "localhost" || numberID(shown["port"]) != "0" || shown["user"] != "local" {
		t.Fatalf("unexpected configured marquee: %#v", shown)
	}
	if shown["billing_runtime_active"] != true || shown["chat_launchable"] != true {
		t.Fatalf("configured marquee should preserve SDK launch-inference fields: %#v", shown)
	}

	res = doReq(t, srv, http.MethodGet, "/api/marquees/legacy-host", nil, "test-token")
	assertErrorCode(t, res, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	res = doReq(t, srv, http.MethodGet, "/api/marquees/"+idString(legacy.ID), nil, "test-token")
	assertErrorCode(t, res, http.StatusNotFound, "RESOURCE_NOT_FOUND")

	var status map[string]any
	res = doReq(t, srv, http.MethodGet, "/api/status", nil, "test-token")
	decodeResp(t, res, &status)
	if got := numberID(status["marquees"]); got != "1" {
		t.Fatalf("expected status marquee count 1, got %s in %#v", got, status)
	}
}

func TestConfiguredMarqueeAcceptsStaleNumericSDKReference(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	domainName := "configured.example.test"
	acme := "ops@example.test"
	configured, err := st.EnsureConfiguredMarquee(context.Background(), domain.Marquee{
		Name:         store.ConfiguredMarqueeName,
		Host:         "localhost",
		User:         "local",
		Port:         0,
		DomainsInput: &domainName,
		AcmeEmail:    &acme,
	})
	if err != nil {
		t.Fatalf("ensure configured marquee: %v", err)
	}
	staleFullFibeID := configured.ID + 10_000

	launchBody := map[string]any{
		"name":              "stale-marquee-launch",
		"marquee_id":        staleFullFibeID,
		"create_playground": false,
		"compose_yaml": `services:
  web:
    image: nginx:alpine
    environment:
      PUBLIC_URL: https://app.$$root_domain
`,
	}
	var launch map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", launchBody, "test-token")
	decodeResp(t, res, &launch)
	playspec, err := st.GetPlayspec(context.Background(), numberID(launch["playspec_id"]))
	if err != nil {
		t.Fatalf("get launch playspec: %v", err)
	}
	if !strings.Contains(playspec.BaseComposeYAML, "PUBLIC_URL: https://app.configured.example.test") {
		t.Fatalf("stale numeric marquee id should compile with configured root domain:\n%s", playspec.BaseComposeYAML)
	}

	playspecBody := map[string]any{"playspec": map[string]any{
		"name":              "stale-marquee-spec",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
	}}
	var createdPlayspec map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &createdPlayspec)
	playgroundBody := map[string]any{"playground": map[string]any{
		"name":        "stale-marquee-pg",
		"playspec_id": "stale-marquee-spec",
		"marquee_id":  staleFullFibeID,
	}}
	var pg map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", playgroundBody, "test-token")
	decodeResp(t, res, &pg)
	if numberID(pg["marquee_id"]) != idString(configured.ID) {
		t.Fatalf("stale numeric marquee id should resolve to configured marquee %d, got %#v", configured.ID, pg)
	}
	assertListNames(t, srv, "/api/playgrounds?marquee_id="+idString(staleFullFibeID), []string{"stale-marquee-pg"})
}

func TestConfiguredMarqueeRejectsNonPositiveExplicitIDs(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	ps, err := st.CreatePlayspec(context.Background(), domain.Playspec{
		Name:            "non-positive-marquee-spec",
		BaseComposeYAML: "services:\n  web:\n    image: alpine\n",
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	for _, id := range []int64{0, -1} {
		t.Run(idString(id), func(t *testing.T) {
			res := doReq(t, srv, http.MethodPost, "/api/playgrounds", map[string]any{
				"playground": map[string]any{
					"name":        fmt.Sprintf("non-positive-marquee-%d", id),
					"playspec_id": *ps.ID,
					"marquee_id":  id,
				},
			}, "test-token")
			defer closeResponseBody(t, res)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("non-positive marquee id should return 400, got %d", res.StatusCode)
			}
		})
	}
}

func TestMarqueeConnectionTestAPIUnsupported(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodPost, "/api/marquees/default/connection_tests", nil, "test-token")
	assertErrorCode(t, res, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestFailedMarqueeDeployPreservesErrorPlayground(t *testing.T) {
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{
			"docker compose -f compose.yml": errors.New("compose failed"),
		},
		ResultContains: map[string]runtime.CommandResult{
			"docker compose -f compose.yml": {Stderr: "bind: address already in use", ExitCode: 1},
		},
	}
	srv, st := newTestServerWithStore(t, fake)
	defer srv.Close()

	playspecBody := map[string]any{"playspec": map[string]any{
		"name": "deploy-failure-spec",
		"base_compose_yaml": `services:
  web:
    image: alpine
`,
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	marquee := ensureTestConfiguredMarquee(t, st)

	pgBody := map[string]any{"playground": map[string]any{
		"name":        "failed-pg",
		"playspec_id": int64(playspec["id"].(float64)),
		"marquee_id":  marquee.ID,
	}}
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", pgBody, "test-token")
	if res.StatusCode < 400 {
		t.Fatalf("expected failed create, got %d", res.StatusCode)
	}
	closeResponseBody(t, res)

	var pg map[string]any
	res = doReq(t, srv, http.MethodGet, "/api/playgrounds/failed-pg", nil, "test-token")
	decodeResp(t, res, &pg)
	if pg["status"] != "error" || pg["error_details"] == nil {
		t.Fatalf("expected persisted error playground, got %#v", pg)
	}
}
