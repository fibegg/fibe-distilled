package domain

import "testing"

func TestParseBuildPlatformAcceptsSupportedValues(t *testing.T) {
	tests := map[string]BuildPlatform{
		"":                BuildPlatformDefault,
		" \t\n":           BuildPlatformDefault,
		"linux/amd64":     BuildPlatformLinuxAMD64,
		" linux/arm64 \n": BuildPlatformLinuxARM64,
	}
	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			platform, err := ParseBuildPlatform(raw)
			if err != nil {
				t.Fatalf("ParseBuildPlatform: %v", err)
			}
			if platform != want || platform.String() != want.String() {
				t.Fatalf("platform = %q, want %q", platform.String(), want.String())
			}
		})
	}
}

func TestParseBuildPlatformRejectsUnsupportedValues(t *testing.T) {
	tests := []string{
		"linux",
		"amd64",
		"linux/amd64/extra/value",
		"linux/amd64 extra",
		"linux/amd_64",
		"linux/x86_64",
		"linux/arm64/v8",
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseBuildPlatform(raw); err == nil {
				t.Fatalf("ParseBuildPlatform(%q) succeeded, want error", raw)
			}
		})
	}
}
