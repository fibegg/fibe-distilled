package runtime

import (
	"context"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// RuntimeArtifactDrift compares generated runtime artifacts with remote files.
func (c Checker) RuntimeArtifactDrift(ctx context.Context, marquee domain.Marquee, project string, composeYAML string) (map[string]string, error) {
	base, err := playgroundBase(project)
	if err != nil {
		return nil, err
	}
	dockerConfig, err := c.dockerConfigJSON()
	if err != nil {
		return nil, err
	}
	reader := remoteFileReaderFor(c.remoteFS())
	return checkRuntimeArtifactDrift(ctx, reader, marquee, runtimeArtifactExpectation{
		Base:         base,
		ComposeYAML:  composeYAML,
		DockerConfig: dockerConfig,
	})
}
