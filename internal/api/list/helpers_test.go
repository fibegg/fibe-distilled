package list

import (
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

type listTestItem struct {
	name      string
	status    string
	createdAt time.Time
}

func TestQueryBoolAndExactParseStrictOptionalValues(t *testing.T) {
	q := url.Values{
		"active": []string{" true "},
		"dry":    []string{"0"},
		"name":   []string{" demo "},
		"blank":  []string{" "},
	}

	active, err := QueryBool(q, "active")
	if err != nil || !active.Present || !active.Value {
		t.Fatalf("active bool = %#v, %v", active, err)
	}
	dry, err := QueryBool(q, "dry")
	if err != nil || !dry.Present || dry.Value {
		t.Fatalf("dry bool = %#v, %v", dry, err)
	}
	name, err := QueryExact(q, "name")
	if err != nil || !name.Present || name.Value != "demo" {
		t.Fatalf("name exact = %#v, %v", name, err)
	}
	if _, err := QueryExact(q, "blank"); err == nil || !strings.Contains(err.Error(), "blank must not be blank") {
		t.Fatalf("expected blank exact error, got %v", err)
	}
	if _, err := QueryBool(url.Values{"active": []string{"maybe"}}, "active"); err == nil {
		t.Fatal("expected invalid bool error")
	}
}

func TestApplyCommonFiltersCreatedWindowAndSorts(t *testing.T) {
	items := []listTestItem{
		{name: "bravo", status: "running", createdAt: time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)},
		{name: "alpha", status: "pending", createdAt: time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)},
		{name: "charlie", status: "running", createdAt: time.Date(2026, 1, 4, 12, 0, 0, 0, time.UTC)},
	}
	r := httptest.NewRequest("GET", "/?created_after=2026-01-02T00:00:00Z&created_before=2026-01-04T00:00:00Z&sort=name_desc", nil)

	got, err := ApplyCommon(r, items, Fields[listTestItem]{
		Name:      func(item listTestItem) string { return item.name },
		Status:    func(item listTestItem) string { return item.status },
		CreatedAt: func(item listTestItem) time.Time { return item.createdAt },
	})
	if err != nil {
		t.Fatalf("ApplyCommon: %v", err)
	}
	gotNames := []string{got[0].name, got[1].name}
	if want := []string{"bravo", "alpha"}; !reflect.DeepEqual(gotNames, want) {
		t.Fatalf("names = %#v, want %#v", gotNames, want)
	}
}

func TestApplyCommonRejectsUnsupportedSortColumn(t *testing.T) {
	r := httptest.NewRequest("GET", "/?sort=status_desc", nil)
	_, err := ApplyNamedCommon(r, []listTestItem{{name: "demo"}}, func(item listTestItem) string {
		return item.name
	}, func(item listTestItem) time.Time {
		return item.createdAt
	})
	if err == nil || !strings.Contains(err.Error(), `sort column "status" is not supported`) {
		t.Fatalf("expected unsupported sort column error, got %v", err)
	}
}
