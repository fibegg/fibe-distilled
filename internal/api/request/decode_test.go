package request

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeAcceptsOneJSONValue(t *testing.T) {
	var payload struct {
		Name string `json:"name"`
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"demo"}`))
	w := httptest.NewRecorder()

	if !Decode(w, r, &payload) {
		t.Fatalf("Decode rejected valid JSON: %s", w.Body.String())
	}
	if payload.Name != "demo" {
		t.Fatalf("decoded name = %q", payload.Name)
	}
}

func TestDecodeRejectsNullAndTrailingJSON(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{name: "null", body: "null", want: "top-level JSON null"},
		{name: "trailing value", body: `{"name":"demo"} {"extra":true}`, want: "multiple JSON values"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var payload map[string]any
			r := httptest.NewRequest("POST", "/", strings.NewReader(tt.body))
			w := httptest.NewRecorder()

			if Decode(w, r, &payload) {
				t.Fatal("Decode accepted invalid JSON")
			}
			if w.Code != 400 || !strings.Contains(w.Body.String(), tt.want) {
				t.Fatalf("response = %d %s, want error containing %q", w.Code, w.Body.String(), tt.want)
			}
		})
	}
}

func TestDecodeOptionalAllowsEmptyBody(t *testing.T) {
	var payload map[string]any
	r := httptest.NewRequest("PATCH", "/", nil)
	w := httptest.NewRecorder()

	if !DecodeOptional(w, r, &payload) {
		t.Fatalf("DecodeOptional rejected empty body: %s", w.Body.String())
	}
	if payload != nil {
		t.Fatalf("empty optional payload = %#v", payload)
	}
}

func TestDecodeJSONStringScalarMapStringifiesScalars(t *testing.T) {
	raw := json.RawMessage(`{"text":"demo","enabled":true,"port":3000,"ratio":1.5}`)

	got := DecodeJSONStringScalarMap(raw, "variables")
	if got.Invalid != "" {
		t.Fatalf("DecodeJSONStringScalarMap invalid: %s", got.Invalid)
	}
	want := map[string]string{"text": "demo", "enabled": "true", "port": "3000", "ratio": "1.5"}
	for key, wantValue := range want {
		if got.Values[key] != wantValue {
			t.Fatalf("%s = %q, want %q in %#v", key, got.Values[key], wantValue, got.Values)
		}
	}

	invalid := DecodeJSONStringScalarMap(json.RawMessage(`{"nested":{"value":1}}`), "variables")
	if invalid.Invalid != "variables.nested must be a string, number, or boolean" {
		t.Fatalf("invalid message = %q", invalid.Invalid)
	}
}

func TestPathValueUsesStdlibPathParams(t *testing.T) {
	r := httptest.NewRequest("GET", "/items/demo", nil)
	r.SetPathValue("identifier", "demo")
	if got := PathValue(r, "identifier"); got != "demo" {
		t.Fatalf("PathValue = %q", got)
	}
}
