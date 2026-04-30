package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
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

	inspect, err := c.internal.ImageInspect(ctx, imageName)
	if err != nil {
		pullResp, pullErr := c.internal.ImagePull(ctx, imageName, client.ImagePullOptions{})
		if pullErr != nil {
			return [32]byte{}, fmt.Errorf("pull image %s: %w", imageName, pullErr)
		}
		pullResp.Wait(ctx)

		inspect, err = c.internal.ImageInspect(ctx, imageName)
		if err != nil {
			return [32]byte{}, fmt.Errorf("inspect image %s: %w", imageName, err)
		}
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

func (c *Client) ListScripts(imageName string) ([]string, error) {
	ctx := context.Background()

	resp, err := c.internal.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:        imageName,
			Entrypoint:   []string{},
			Cmd:          []string{"ls", "/scripts"},
			Tty:          false,
			AttachStdout: true,
		},
		HostConfig: &container.HostConfig{},
	})
	if err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}
	defer func() {
		_, _ = c.internal.ContainerRemove(ctx, resp.ID, client.ContainerRemoveOptions{
			Force: true,
		})
	}()

	if _, err := c.internal.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return nil, fmt.Errorf("start container: %w", err)
	}

	var out bytes.Buffer

	rc, attErr := c.internal.ContainerLogs(ctx, resp.ID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})

	if attErr == nil {
		_ = demuxDockerStream(rc, &out)
	}

	waitResult := c.internal.ContainerWait(ctx, resp.ID, client.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	select {
	case chErr := <-waitResult.Error:
		if chErr != nil {
			return nil, fmt.Errorf("wait container: %w", chErr)
		}
	case status := <-waitResult.Result:
		if status.StatusCode != 0 {
			return nil, fmt.Errorf("container exited with code %d: %s", status.StatusCode, out.String())
		}
	}

	var scripts []string
	for _, line := range bytes.Split(out.Bytes(), []byte("\n")) {
		name := bytes.TrimSpace(line)
		if len(name) > 0 {
			scripts = append(scripts, string(name))
		}
	}

	return scripts, nil
}

func (c *Client) RunScript(imageName string, script string, args []string) error {
	ctx := context.Background()

	cmd := append([]string{"/scripts/" + script}, args...)

	resp, err := c.internal.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:        imageName,
			Entrypoint:   cmd[:1],
			Cmd:          cmd[1:],
			Tty:          false,
			AttachStdout: true,
			AttachStderr: true,
		},
		HostConfig: &container.HostConfig{},
	})
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	defer func() {
		_, _ = c.internal.ContainerRemove(ctx, resp.ID, client.ContainerRemoveOptions{
			Force: true,
		})
	}()

	if _, err := c.internal.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	rc, err := c.internal.ContainerLogs(ctx, resp.ID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err == nil {
		_ = demuxDockerStream(rc, os.Stdout)
	}

	waitResult := c.internal.ContainerWait(ctx, resp.ID, client.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	select {
	case chErr := <-waitResult.Error:
		if chErr != nil {
			return fmt.Errorf("wait container: %w", chErr)
		}
	case status := <-waitResult.Result:
		if status.StatusCode != 0 {
			return fmt.Errorf("container exited with code %d", status.StatusCode)
		}
	}

	return nil
}

func demuxDockerStream(r io.Reader, w io.Writer) error {
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, header); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		size := binary.BigEndian.Uint32(header[4:])
		if _, err := io.CopyN(w, r, int64(size)); err != nil {
			return err
		}
	}
}
