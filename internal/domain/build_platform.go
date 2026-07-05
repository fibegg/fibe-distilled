package domain

import (
	"errors"
	"strings"
)

// BuildPlatform is a supported Docker build platform selector.
type BuildPlatform string

const (
	// BuildPlatformDefault omits Docker's --platform flag and uses the host default.
	BuildPlatformDefault BuildPlatform = ""
	// BuildPlatformLinuxAMD64 selects the Linux amd64 build platform.
	BuildPlatformLinuxAMD64 BuildPlatform = "linux/amd64"
	// BuildPlatformLinuxARM64 selects the Linux arm64 build platform.
	BuildPlatformLinuxARM64 BuildPlatform = "linux/arm64"
)

// ParseBuildPlatform validates a fibe-distilled Docker build platform.
func ParseBuildPlatform(raw string) (BuildPlatform, error) {
	raw = strings.TrimSpace(raw)
	switch BuildPlatform(raw) {
	case BuildPlatformDefault, BuildPlatformLinuxAMD64, BuildPlatformLinuxARM64:
		return BuildPlatform(raw), nil
	default:
		return "", errors.New("build platform must be linux/amd64 or linux/arm64")
	}
}

// String returns the Docker CLI platform string.
func (p BuildPlatform) String() string {
	return string(p)
}
