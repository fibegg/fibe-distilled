package runtime

import (
	"strings"
	"testing"
)

func TestDeterministicImageRefSanitizesComponentsAndTag(t *testing.T) {
	got, err := DeterministicImageRef(" Demo / App ", " Web/API ", " abc/DEF@123 ")
	if err != nil {
		t.Fatalf("DeterministicImageRef() error = %v", err)
	}
	want := "fibe-distilled/demo-app/web-api:abcDEF123"
	if got != want {
		t.Fatalf("DeterministicImageRef() = %q, want %q", got, want)
	}
}

func TestDeterministicImageRefRejectsEmptyComponents(t *testing.T) {
	tests := []struct {
		name    string
		project string
		service string
		tag     string
		want    string
	}{
		{name: "project", project: " / ", service: "web", tag: "abc123", want: "project component"},
		{name: "service", project: "demo", service: " @ ", tag: "abc123", want: "service component"},
		{name: "tag", project: "demo", service: "web", tag: " .- ", want: "tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DeterministicImageRef(tt.project, tt.service, tt.tag)
			if err == nil {
				t.Fatal("DeterministicImageRef() error = nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("DeterministicImageRef() error = %q, want %q", err, tt.want)
			}
		})
	}
}
