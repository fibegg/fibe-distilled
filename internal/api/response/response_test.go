package response

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONReturnsServerErrorForUnencodablePayload(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()

	JSON(rec, req, http.StatusOK, map[string]any{"bad": math.Inf(1)})

	res := rec.Result()
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 for unencodable payload, got %d", res.StatusCode)
	}
	if !strings.Contains(rec.Body.String(), "INTERNAL_ERROR") {
		t.Fatalf("expected structured internal error, got %s", rec.Body.String())
	}
}

func closeResponseBody(t *testing.T, res *http.Response) {
	t.Helper()
	if err := res.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}
}

func TestListPaginationUsesFibeDefaultsAndCaps(t *testing.T) {
	items := make([]string, 150)
	for i := range items {
		items[i] = "item"
	}

	for _, tc := range []struct {
		name string
		path string
		data []string
		meta listMeta
		rows int
	}{
		{name: "empty defaults", path: "/api/things", data: nil, meta: listMeta{Page: 1, PerPage: 25, Total: 0}, rows: 0},
		{name: "per page cap", path: "/api/things?per_page=500", data: items, meta: listMeta{Page: 1, PerPage: 100, Total: 150}, rows: 100},
		{name: "page cap", path: "/api/things?page=5000&per_page=1", data: items, meta: listMeta{Page: 1000, PerPage: 1, Total: 150}, rows: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := listEnvelopeForPath(t, tc.path, tc.data)
			assertListEnvelope(t, got, tc.meta, tc.rows)
		})
	}
}

func TestListPaginationRejectsMalformedValues(t *testing.T) {
	for _, path := range []string{
		"/api/things?page=",
		"/api/things?page=0",
		"/api/things?page=abc",
		"/api/things?per_page=",
		"/api/things?per_page=0",
		"/api/things?per_page=abc",
	} {
		rec := httptest.NewRecorder()
		List(rec, httptest.NewRequest(http.MethodGet, path, nil), []string{"item"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s should return 400, got %d", path, rec.Code)
		}
	}
}

func decodeTestJSON(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func listEnvelopeForPath(t *testing.T, path string, data []string) listEnvelope[string] {
	t.Helper()
	rec := httptest.NewRecorder()
	List(rec, httptest.NewRequest(http.MethodGet, path, nil), data)
	var got listEnvelope[string]
	decodeTestJSON(t, rec, &got)
	return got
}

func assertListEnvelope(t *testing.T, got listEnvelope[string], meta listMeta, rows int) {
	t.Helper()
	if got.Meta != meta || len(got.Data) != rows {
		t.Fatalf("list envelope = meta=%#v len=%d, want meta=%#v len=%d", got.Meta, len(got.Data), meta, rows)
	}
}
