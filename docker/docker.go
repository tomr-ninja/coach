package docker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

type Client struct {
	internal *client.Client
}

func NewRealDockerClient() (*Client, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	return &Client{internal: cli}, nil
}

func (c *Client) ImageDigest(imageName string) ([32]byte, error) {
	ctx := context.Background()

	pullResp, err := c.internal.ImagePull(ctx, imageName, client.ImagePullOptions{})
	if err != nil {
		return [32]byte{}, fmt.Errorf("pull image %s: %w", imageName, err)
	}
	pullResp.Wait(ctx)

	inspect, err := c.internal.ImageInspect(ctx, imageName)
	if err != nil {
		return [32]byte{}, fmt.Errorf("inspect image %s: %w", imageName, err)
	}

	var digestStr string
	if len(inspect.RepoDigests) > 0 {
		digestStr = inspect.RepoDigests[0]
	} else if inspect.ID != "" {
		digestStr = inspect.ID
	} else {
		return [32]byte{}, fmt.Errorf("no digest found for image %s", imageName)
	}

	return sha256.Sum256([]byte(digestStr)), nil
}

func (c *Client) Run(imageName, dataDir, outputDir string) error {
	ctx := context.Background()

	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("resolve output dir: %w", err)
	}

	resp, err := c.internal.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: imageName,
		},
		HostConfig: &container.HostConfig{
			Mounts: []mount.Mount{
				{
					Type:     mount.TypeBind,
					Source:   absData,
					Target:   "/data",
					ReadOnly: true,
				},
				{
					Type:   mount.TypeBind,
					Source: absOutput,
					Target: "/output",
				},
			},
			AutoRemove: true,
		},
	})
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}

	if _, err := c.internal.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	waitResult := c.internal.ContainerWait(ctx, resp.ID, client.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	select {
	case err := <-waitResult.Error:
		if err != nil {
			return fmt.Errorf("wait container: %w", err)
		}
	case status := <-waitResult.Result:
		if status.StatusCode != 0 {
			return fmt.Errorf("container exited with code %d", status.StatusCode)
		}
	}

	return nil
}

func (c *Client) Close() error {
	return c.internal.Close()
}
