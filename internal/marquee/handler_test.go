package marquee

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/storage"
)

type fakeMarqueeRepo struct {
	configured    domain.Marquee
	configuredOK  bool
	configuredErr error
	byIdentifier  map[string]domain.Marquee
	getErr        error
}

func (f fakeMarqueeRepo) GetRuntimeMarquee(context.Context) (domain.Marquee, bool, error) {
	return f.configured, f.configuredOK, f.configuredErr
}

func (f fakeMarqueeRepo) GetMarquee(_ context.Context, identifier string) (domain.Marquee, error) {
	if f.getErr != nil {
		return domain.Marquee{}, f.getErr
	}
	if item, ok := f.byIdentifier[identifier]; ok {
		return item, nil
	}
	return domain.Marquee{}, storage.ErrNotFound
}

func TestConfiguredListAndVisibleOnlyExposeConfiguredMarquee(t *testing.T) {
	configured := domain.Marquee{ID: 7, Name: "default", Status: "running"}
	handler := NewHandler(fakeMarqueeRepo{configured: configured, configuredOK: true})

	items, err := handler.ConfiguredList(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != 7 {
		t.Fatalf("ConfiguredList = %#v, %v", items, err)
	}
	visible, err := handler.Visible(context.Background(), domain.Marquee{ID: 7})
	if err != nil || !visible {
		t.Fatalf("configured marquee should be visible, got %v, %v", visible, err)
	}
	visible, err = handler.Visible(context.Background(), domain.Marquee{ID: 8})
	if err != nil || visible {
		t.Fatalf("other marquee should be hidden, got %v, %v", visible, err)
	}

	empty := NewHandler(fakeMarqueeRepo{})
	items, err = empty.ConfiguredList(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("empty ConfiguredList = %#v, %v", items, err)
	}
}

func TestResolveConfiguredIDAliasesAndRejectsOutOfScopeReferences(t *testing.T) {
	configured := domain.Marquee{ID: 7, Name: "default"}
	handler := NewHandler(fakeMarqueeRepo{
		configured:   configured,
		configuredOK: true,
		byIdentifier: map[string]domain.Marquee{
			"default": configured,
			"other":   {ID: 8, Name: "other"},
		},
	})

	got, err := handler.ResolveConfiguredID(context.Background(), nil, "")
	if err != nil || got == nil || *got != 7 {
		t.Fatalf("default configured id = %v, %v", got, err)
	}
	explicit := int64(99)
	got, err = handler.ResolveConfiguredID(context.Background(), &explicit, "")
	if err != nil || got == nil || *got != 7 {
		t.Fatalf("explicit configured id alias = %v, %v", got, err)
	}
	zero := int64(0)
	if _, err := handler.ResolveConfiguredID(context.Background(), &zero, ""); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("zero explicit id error = %v", err)
	}
	got, err = handler.ResolveConfiguredID(context.Background(), nil, "default")
	if err != nil || got == nil || *got != 7 {
		t.Fatalf("named configured id = %v, %v", got, err)
	}
	if _, err := handler.ResolveConfiguredID(context.Background(), nil, "other"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("out-of-scope marquee error = %v", err)
	}
}

func TestListFiltersAndSortsConfiguredMarquee(t *testing.T) {
	configured := domain.Marquee{
		ID:        7,
		Name:      "default",
		Status:    "running",
		CreatedAt: time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC),
	}
	handler := NewHandler(fakeMarqueeRepo{configured: configured, configuredOK: true})
	r := httptest.NewRequest(http.MethodGet, "/api/marquees?status=running&name=def&sort=name_asc", nil)
	w := httptest.NewRecorder()

	handler.List(w, r)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"default"`) {
		t.Fatalf("list response = %d %s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/marquees?status=", nil)
	w = httptest.NewRecorder()
	handler.List(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "status must not be blank") {
		t.Fatalf("bad filter response = %d %s", w.Code, w.Body.String())
	}
}
