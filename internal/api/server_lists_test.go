package api

import (
	"context"
	"net/http"
	"testing"

	store "github.com/fibegg/fibe-distilled/internal/storage"
)

func TestPlaygroundListFiltersByPlayspecAndMarqueeID(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	marquee := ensureTestConfiguredMarquee(t, st)

	createPlayspec := func(name string) map[string]any {
		t.Helper()
		body := map[string]any{"playspec": map[string]any{
			"name": name,
			"base_compose_yaml": `services:
  web:
    image: alpine
`,
		}}
		var playspec map[string]any
		res := doReq(t, srv, http.MethodPost, "/api/playspecs", body, "test-token")
		decodeResp(t, res, &playspec)
		return playspec
	}

	first := createPlayspec("filter-one")
	second := createPlayspec("filter-two")

	for _, body := range []map[string]any{
		{"playground": map[string]any{"name": "filter-pg-one", "playspec_id": int64(first["id"].(float64))}},
		{"playground": map[string]any{"name": "filter-pg-two", "playspec_id": int64(second["id"].(float64)), "marquee_id": marquee.ID}},
	} {
		var pg map[string]any
		res := doReq(t, srv, http.MethodPost, "/api/playgrounds", body, "test-token")
		decodeResp(t, res, &pg)
	}

	assertListNames(t, srv, "/api/playgrounds?playspec_id="+numberID(first["id"]), []string{"filter-pg-one"})
	assertListNames(t, srv, "/api/playgrounds?marquee_id="+idString(marquee.ID), []string{"filter-pg-one", "filter-pg-two"})
	assertListNames(t, srv, "/api/playgrounds?playspec_id=filter-one", []string{"filter-pg-one"})
	assertListNames(t, srv, "/api/playgrounds?marquee_id="+store.ConfiguredMarqueeName, []string{"filter-pg-one", "filter-pg-two"})
	assertListNames(t, srv, "/api/playgrounds?playspec_id=missing-filter-spec", nil)
	assertListNames(t, srv, "/api/playgrounds?marquee_id=missing-filter-marquee", nil)
	assertListNames(t, srv, "/api/playgrounds?playspec_id=not-a-number", nil)

	for _, path := range []string{
		"/api/playgrounds?playspec_id=",
		"/api/playgrounds?playspec_id=0",
		"/api/playgrounds?playspec_id=999999999999999999999999999999999999999",
		"/api/playgrounds?marquee_id=",
		"/api/playgrounds?marquee_id=-1",
	} {
		res := doReq(t, srv, http.MethodGet, path, nil, "test-token")
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s should reject invalid resource filter values, got %d", path, res.StatusCode)
		}
		closeResponseBody(t, res)
	}
}

func TestResourceListFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	seedResourceListFilterRows(t, ctx, st)
	assertSupportedResourceListFilters(t, srv)
	assertInvalidResourceListFilters(t, srv)
	assertUnsupportedResourceListFilters(t, srv)
}
