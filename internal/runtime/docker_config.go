package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// writeDockerConfigDir writes Docker registry credentials for remote commands.
func (c Checker) writeDockerConfigDir(ctx context.Context, fsys RemoteFS, marquee domain.Marquee, dir string) error {
	dockerConfig, err := c.dockerConfigJSON()
	if err != nil {
		return err
	}
	configPath := dir + "/config.json"
	return fsys.WriteRemoteFile(ctx, marquee, configPath, []byte(dockerConfig), 0o600)
}

// dockerConfigJSON renders process-level DockerHub credentials for Docker CLI.
func (c Checker) dockerConfigJSON() (string, error) {
	username := strings.TrimSpace(c.DockerHubUsername)
	token := strings.TrimSpace(c.DockerHubToken)
	if username == "" || token == "" {
		return "{}", nil
	}
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + token))
	config := map[string]any{
		"auths": map[string]any{
			"https://index.docker.io/v1/": map[string]string{"auth": auth},
			"registry-1.docker.io":        map[string]string{"auth": auth},
		},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
