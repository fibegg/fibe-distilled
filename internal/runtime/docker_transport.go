package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
)

// dockerSocketHost is the Docker SDK endpoint for the required local daemon socket.
const dockerSocketHost = "unix:///var/run/docker.sock"

// DockerTransport uses the Docker Engine SDK over the local daemon socket.
type DockerTransport struct {
	// DockerHubUsername authenticates image pulls when set.
	DockerHubUsername string
	// DockerHubToken authenticates image pulls when set.
	DockerHubToken string
}

// Ping verifies the local Docker daemon is reachable.
func (t DockerTransport) Ping(ctx context.Context, marquee domain.Marquee) error {
	return t.withClient(marquee, func(client *dockerclient.Client) error {
		_, err := client.Ping(ctx)
		return err
	})
}

// ImageMetadata reads fibe-distilled build labels from a local image.
func (t DockerTransport) ImageMetadata(ctx context.Context, marquee domain.Marquee, imageRef string) (ImageMetadata, bool, error) {
	var metadata ImageMetadata
	found := false
	err := t.withClient(marquee, func(client *dockerclient.Client) error {
		inspect, err := client.ImageInspect(ctx, imageRef)
		if err != nil {
			if cerrdefs.IsNotFound(err) {
				return nil
			}
			return err
		}
		found = true
		labels := map[string]string{}
		if inspect.Config != nil && inspect.Config.Labels != nil {
			labels = inspect.Config.Labels
		}
		metadata = ImageMetadata{
			CommitSHA:           firstStringLabelValue(labels, "fibe.build.git_commit_sha", "fibe.source_commit"),
			BuildIdentityDigest: stringLabelValue(labels, "fibe.build.identity_digest"),
		}
		return nil
	})
	return metadata, found, err
}

// EnsureTraefik pulls and starts the managed Traefik container when needed.
func (t DockerTransport) EnsureTraefik(ctx context.Context, marquee domain.Marquee, args []string) error {
	return t.withClient(marquee, func(client *dockerclient.Client) error {
		if err := t.pullImage(ctx, client, traefikImage); err != nil {
			return err
		}
		if ready, err := traefikContainerReady(ctx, client, args); err != nil {
			return err
		} else if ready {
			return nil
		}
		if err := removeContainerIfPresent(ctx, client, "fibe-distilled-traefik"); err != nil {
			return err
		}
		config := &container.Config{
			Image:  traefikImage,
			Env:    []string{"DOCKER_API_VERSION=" + traefikDockerAPIVersion},
			Labels: map[string]string{fibeDistilledManagedLabel: "true"},
			Cmd:    args,
		}
		hostConfig := &container.HostConfig{
			Binds: []string{
				"/var/run/docker.sock:/var/run/docker.sock:ro",
				optfibe.TraefikPath + ":/etc/traefik",
			},
			NetworkMode:   container.NetworkMode("host"),
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		}
		created, err := client.ContainerCreate(ctx, config, hostConfig, nil, nil, "fibe-distilled-traefik")
		if err != nil {
			return err
		}
		return client.ContainerStart(ctx, created.ID, container.StartOptions{})
	})
}

// CleanupProject removes Compose-owned leftovers for a project.
func (t DockerTransport) CleanupProject(ctx context.Context, marquee domain.Marquee, project string, removeVolumes bool) error {
	return t.withClient(marquee, func(client *dockerclient.Client) error {
		if err := cleanupProjectContainers(ctx, client, project); err != nil {
			return err
		}
		if err := cleanupProjectNetworks(ctx, client, project); err != nil {
			return err
		}
		if removeVolumes {
			return cleanupProjectVolumes(ctx, client, project)
		}
		return nil
	})
}

// withClient opens a Docker SDK client over /var/run/docker.sock.
func (t DockerTransport) withClient(_ domain.Marquee, run func(*dockerclient.Client) error) error {
	client, err := dockerclient.NewClientWithOpts(
		dockerclient.WithHost(dockerSocketHost),
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return err
	}
	defer closeResource(client)
	return run(client)
}

// pullImage pulls an image with DockerHub credentials when configured.
func (t DockerTransport) pullImage(ctx context.Context, client *dockerclient.Client, imageRef string) error {
	stream, err := client.ImagePull(ctx, imageRef, image.PullOptions{RegistryAuth: t.registryAuthHeader()})
	if err != nil {
		return err
	}
	defer closeResource(stream)
	_, err = io.Copy(io.Discard, stream)
	return err
}

// registryAuthHeader returns the Docker SDK auth header payload.
func (t DockerTransport) registryAuthHeader() string {
	username := strings.TrimSpace(t.DockerHubUsername)
	token := strings.TrimSpace(t.DockerHubToken)
	if username == "" || token == "" {
		return ""
	}
	// #nosec G117 -- registry auth JSON is passed directly to the Docker SDK and is never logged.
	raw, err := json.Marshal(registry.AuthConfig{
		Username:      username,
		Password:      token,
		ServerAddress: "https://index.docker.io/v1/",
	})
	if err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(raw)
}

// traefikContainerReady checks whether the current managed Traefik container matches expected config.
func traefikContainerReady(ctx context.Context, client *dockerclient.Client, args []string) (bool, error) {
	inspect, err := client.ContainerInspect(ctx, "fibe-distilled-traefik")
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if !inspect.State.Running {
		return false, nil
	}
	if inspect.Config == nil {
		return false, nil
	}
	if inspect.Config.Labels[fibeDistilledManagedLabel] != "true" {
		return false, nil
	}
	if !slices.Contains(inspect.Config.Env, "DOCKER_API_VERSION="+traefikDockerAPIVersion) {
		return false, nil
	}
	return reflect.DeepEqual(inspect.Args, args), nil
}

// removeContainerIfPresent force-removes a container when it exists.
func removeContainerIfPresent(ctx context.Context, client *dockerclient.Client, name string) error {
	err := client.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// cleanupProjectContainers removes Compose containers for a project.
func cleanupProjectContainers(ctx context.Context, client *dockerclient.Client, project string) error {
	args := filters.NewArgs(filters.Arg("label", "com.docker.compose.project="+project))
	containers, err := client.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return err
	}
	for _, summary := range containers {
		if err := client.ContainerRemove(ctx, summary.ID, container.RemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// cleanupProjectNetworks removes Compose networks for a project.
func cleanupProjectNetworks(ctx context.Context, client *dockerclient.Client, project string) error {
	args := filters.NewArgs(filters.Arg("label", "com.docker.compose.project="+project))
	networks, err := client.NetworkList(ctx, network.ListOptions{Filters: args})
	if err != nil {
		return err
	}
	for _, item := range networks {
		if err := client.NetworkRemove(ctx, item.ID); err != nil && !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("remove network %s: %w", item.Name, err)
		}
	}
	return nil
}

// cleanupProjectVolumes removes Compose volumes for a project.
func cleanupProjectVolumes(ctx context.Context, client *dockerclient.Client, project string) error {
	args := filters.NewArgs(filters.Arg("label", "com.docker.compose.project="+project))
	volumes, err := client.VolumeList(ctx, volume.ListOptions{Filters: args})
	if err != nil {
		return err
	}
	for _, item := range volumes.Volumes {
		if item == nil {
			continue
		}
		if err := client.VolumeRemove(ctx, item.Name, true); err != nil && !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("remove volume %s: %w", item.Name, err)
		}
	}
	return nil
}

// firstStringLabelValue returns the first nonblank Docker label value.
func firstStringLabelValue(labels map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := stringLabelValue(labels, key); value != "" {
			return value
		}
	}
	return ""
}

// stringLabelValue trims one Docker label value.
func stringLabelValue(labels map[string]string, key string) string {
	return strings.TrimSpace(labels[key])
}
