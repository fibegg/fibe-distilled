package buildrecord

import (
	"strings"
	"testing"
)

func TestApplyBuildImagesReplacesBuildWithPinnedImage(t *testing.T) {
	rendered, err := ApplyBuildImages(`services:
  web:
    build: .
    image: local
`, map[string]string{"web": "fibe-distilled/demo/web:abc123"})
	if err != nil {
		t.Fatalf("apply build images: %v", err)
	}
	if strings.Contains(rendered, "build:") {
		t.Fatalf("expected build to be removed:\n%s", rendered)
	}
	for _, want := range []string{"image: fibe-distilled/demo/web:abc123", "pull_policy: never"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered compose:\n%s", want, rendered)
		}
	}
}

func TestApplyBuildImagesFailsWhenBuiltServiceIsMissing(t *testing.T) {
	_, err := ApplyBuildImages(`services:
  web:
    image: nginx
`, map[string]string{"worker": "fibe-distilled/demo/worker:abc123"})
	if err == nil || !strings.Contains(err.Error(), `built service "worker" missing`) {
		t.Fatalf("expected missing built service error, got %v", err)
	}
}

func TestApplyBuildImagesFailsClosedOnMalformedCompose(t *testing.T) {
	for _, tt := range []struct {
		name    string
		compose string
		want    string
	}{
		{
			name:    "top-level null",
			compose: "null\n",
			want:    "compose yaml must be a mapping",
		},
		{
			name: "null service body",
			compose: `services:
  web: null
`,
			want: `compose service "web" must be a mapping`,
		},
		{
			name: "missing services",
			compose: `volumes:
  data: {}
`,
			want: `built service "web" missing`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ApplyBuildImages(tt.compose, map[string]string{"web": "fibe-distilled/demo/web:abc123"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}
