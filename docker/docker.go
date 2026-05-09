package docker

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	coacherrors "github.com/tomr-ninja/coach/internal/errors"
	"github.com/tomr-ninja/coach/internal/utils"
)

var (
	errNoDigest      = errors.New("no digest found for image")
	errContainerExit = coacherrors.New(coacherrors.KindIO, "container exited with non-zero code")
	errDockerStream  = errors.New("docker stream contains error")
)

type Client struct {
	internal *client.Client
}

func NewClient() (*Client, error) {
	return coacherrors.Retry(context.Background(), coacherrors.RetryConfig{MaxElapsed: 5 * time.Second}, func() (*Client, error) {
		cli, err := client.New(client.FromEnv)
		if err != nil {
			wrapped := fmt.Errorf("create docker client: %w", err)
			if client.IsErrConnectionFailed(err) {
				return nil, coacherrors.WithHint(wrapped, "Ensure Docker is running and available at DOCKER_HOST.")
			}
			return nil, wrapped
		}
		return &Client{internal: cli}, nil
	})
}

func (c *Client) ImageExists(ctx context.Context, imageName string) (bool, error) {
	_, err := c.internal.ImageInspect(ctx, imageName)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func (c *Client) EnsureImageDigest(ctx context.Context, imageName, platform string) ([32]byte, error) {
	inspect, err := c.internal.ImageInspect(ctx, imageName)
	if err != nil {
		pullResp, pullErr := c.internal.ImagePull(ctx, imageName, client.ImagePullOptions{
			Platforms: parsePlatforms(platform),
		})
		if pullErr != nil {
			return [32]byte{}, fmt.Errorf("pull image %s: %w", imageName, pullErr)
		}
		if waitErr := pullResp.Wait(ctx); waitErr != nil {
			return [32]byte{}, fmt.Errorf("pull image %s (wait): %w", imageName, waitErr)
		}

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
		return [32]byte{}, fmt.Errorf("%w: %s", errNoDigest, imageName)
	}

	return sha256.Sum256([]byte(digestStr)), nil
}

func (c *Client) ImageBuild(ctx context.Context, buildContext io.Reader, tag, platform string) error {
	suppress := !utils.IsVerbose(ctx)
	resp, err := c.internal.ImageBuild(ctx, buildContext, client.ImageBuildOptions{
		Tags:           []string{tag},
		SuppressOutput: suppress,
		Remove:         true,
		Platforms:      parsePlatforms(platform),
	})
	if err != nil {
		return fmt.Errorf("build image %s: %w", tag, err)
	}
	defer resp.Body.Close()
	if streamErr := parseDockerStream(resp.Body, os.Stderr); streamErr != nil {
		return fmt.Errorf("build image %s: %w", tag, streamErr)
	}
	return nil
}

func (c *Client) ImagePush(ctx context.Context, tag, auth string) error {
	resp, err := c.internal.ImagePush(ctx, tag, client.ImagePushOptions{
		All:          false,
		RegistryAuth: registryAuth(tag, auth),
	})
	if err != nil {
		return fmt.Errorf("push image %s: %w", tag, err)
	}
	defer resp.Close()
	if streamErr := parseDockerStream(resp, os.Stderr); streamErr != nil {
		return fmt.Errorf("push image %s: %w", tag, streamErr)
	}
	return nil
}

func (c *Client) ImageRemove(ctx context.Context, imageID string, force bool) error {
	_, err := c.internal.ImageRemove(ctx, imageID, client.ImageRemoveOptions{
		Force:         force,
		PruneChildren: true,
	})
	if err != nil {
		return fmt.Errorf("remove image %s: %w", imageID, err)
	}
	return nil
}

func (c *Client) ImageList(ctx context.Context, filterRef string) ([]image.Summary, error) {
	opts := client.ImageListOptions{All: true}
	if filterRef != "" {
		opts.Filters = make(client.Filters).Add("reference", filterRef)
	}
	result, err := c.internal.ImageList(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	return result.Items, nil
}

func registryAuth(imageTag, authStr string) string {
	if authStr != "" {
		parts := strings.SplitN(authStr, ":", 2)
		username := parts[0]
		password := ""
		if len(parts) == 2 {
			password = parts[1]
		}

		server, _, _ := strings.Cut(imageTag, "/")

		auth := struct {
			Username      string `json:"username"`
			Password      string `json:"password"`
			ServerAddress string `json:"serveraddress,omitempty"`
		}{
			Username:      username,
			Password:      password,
			ServerAddress: server,
		}

		data, err := json.Marshal(auth) // #nosec G117
		if err != nil {
			return ""
		}

		return base64.URLEncoding.EncodeToString(data)
	}

	return ""
}

func BuildContextDir(dir string) (_ io.Reader, retErr error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	defer func() {
		if closeErr := tw.Close(); closeErr != nil && retErr == nil {
			retErr = fmt.Errorf("close tar: %w", closeErr)
		}
	}()

	if walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		header, hdrErr := tar.FileInfoHeader(info, "")
		if hdrErr != nil {
			return hdrErr
		}
		header.Name = rel
		writeErr := tw.WriteHeader(header)
		if writeErr != nil {
			return writeErr
		}
		if info.IsDir() {
			return nil
		}
		f, openErr := os.Open(path) //nolint:gosec // G122: build context from trusted temp dir
		if openErr != nil {
			return openErr
		}
		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}

		return closeErr
	}); walkErr != nil {
		return nil, fmt.Errorf("build context: %w", walkErr)
	}

	return &buf, nil
}

func (c *Client) Run(ctx context.Context, imageName, dataDir, outputDir string) error {
	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	if _, statErr := os.Stat(absData); statErr != nil {
		return fmt.Errorf("data dir %s does not exist: %w", absData, statErr)
	}
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("resolve output dir: %w", err)
	}
	if _, statErr := os.Stat(absOutput); statErr != nil {
		return fmt.Errorf("output dir %s does not exist: %w", absOutput, statErr)
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
			AutoRemove: false,
		},
	})
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	defer func() {
		if utils.IsVerbose(ctx) {
			fmt.Fprintf(os.Stderr, "removing container %s\n", resp.ID[:12])
		}
		_, _ = c.internal.ContainerRemove(context.Background(), resp.ID, client.ContainerRemoveOptions{Force: true})
	}()

	if utils.IsVerbose(ctx) {
		fmt.Fprintf(os.Stderr, "starting container %s (%s)\n", resp.ID[:12], imageName)
	}
	if _, err := c.internal.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	waitResult := c.internal.ContainerWait(ctx, resp.ID, client.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-waitResult.Error:
		if err != nil {
			return fmt.Errorf("wait container: %w", err)
		}
	case status := <-waitResult.Result:
		if status.StatusCode != 0 {
			logs, _ := c.containerLogs(ctx, resp.ID)
			if logs != "" {
				return fmt.Errorf("%w: %d\n%s", errContainerExit, status.StatusCode, logs)
			}
			return fmt.Errorf("%w: %d", errContainerExit, status.StatusCode)
		}
	}

	return nil
}

func (c *Client) ImageEntrypoint(ctx context.Context, imageName string) (entrypoint []string, cmd []string, err error) {
	inspect, err := c.internal.ImageInspect(ctx, imageName)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect image %s: %w", imageName, err)
	}
	return inspect.Config.Entrypoint, inspect.Config.Cmd, nil
}

func (c *Client) RunWrapped(ctx context.Context, imageName string, envVars map[string]string) error {
	env := make([]string, 0, len(envVars))
	for k, v := range envVars {
		env = append(env, k+"="+v)
	}

	resp, err := c.internal.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: imageName,
			Env:   env,
		},
		HostConfig: &container.HostConfig{
			AutoRemove: false,
		},
	})
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	defer func() {
		if utils.IsVerbose(ctx) {
			fmt.Fprintf(os.Stderr, "removing container %s\n", resp.ID[:12])
		}
		_, _ = c.internal.ContainerRemove(context.Background(), resp.ID, client.ContainerRemoveOptions{Force: true})
	}()

	if utils.IsVerbose(ctx) {
		fmt.Fprintf(os.Stderr, "starting container %s (%s)\n", resp.ID[:12], imageName)
	}
	if _, err := c.internal.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	waitResult := c.internal.ContainerWait(ctx, resp.ID, client.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-waitResult.Error:
		if err != nil {
			return fmt.Errorf("wait container: %w", err)
		}
	case status := <-waitResult.Result:
		if status.StatusCode != 0 {
			logs, _ := c.containerLogs(ctx, resp.ID)
			if logs != "" {
				return fmt.Errorf("%w: %d\n%s", errContainerExit, status.StatusCode, logs)
			}
			return fmt.Errorf("%w: %d", errContainerExit, status.StatusCode)
		}
	}

	return nil
}

func (c *Client) containerLogs(ctx context.Context, containerID string) (string, error) {
	rc, err := c.internal.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		return "", err
	}
	defer rc.Close()

	var buf bytes.Buffer
	if err := demuxDockerStream(rc, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ImagePlatform returns the platform of a locally available image
// in "os/arch" format (e.g., "linux/amd64").
func (c *Client) ImagePlatform(ctx context.Context, imageName string) (string, error) {
	inspect, err := c.internal.ImageInspect(ctx, imageName)
	if err != nil {
		return "", fmt.Errorf("inspect image %s: %w", imageName, err)
	}
	return inspect.Os + "/" + inspect.Architecture, nil
}

func parsePlatforms(platform string) []ocispec.Platform {
	if platform == "" {
		return nil
	}
	os, arch, _ := strings.Cut(platform, "/")

	return []ocispec.Platform{{OS: os, Architecture: arch}}
}

func (c *Client) Close() error {
	return c.internal.Close()
}

func (c *Client) ListScripts(ctx context.Context, imageName string) ([]string, error) {
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
		defer rc.Close()
		if err := demuxDockerStream(rc, &out); err != nil {
			return nil, fmt.Errorf("read container logs: %w", err)
		}
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
			return nil, fmt.Errorf("%w: %d: %s", errContainerExit, status.StatusCode, out.String())
		}
	}

	var scripts []string
	for line := range bytes.Lines(out.Bytes()) {
		name := bytes.TrimSpace(line)
		if len(name) > 0 {
			scripts = append(scripts, string(name))
		}
	}

	return scripts, nil
}

func (c *Client) RunScript(ctx context.Context, imageName string, script string, args []string, dataDir string, outputDir string) error {
	cmd := append([]string{"/scripts/" + script}, args...)

	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	if _, statErr := os.Stat(absData); statErr != nil {
		return fmt.Errorf("data dir %s does not exist: %w", absData, statErr)
	}
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("resolve output dir: %w", err)
	}
	if _, statErr := os.Stat(absOutput); statErr != nil {
		return fmt.Errorf("output dir %s does not exist: %w", absOutput, statErr)
	}

	resp, err := c.internal.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:        imageName,
			Entrypoint:   cmd[:1],
			Cmd:          cmd[1:],
			Tty:          false,
			AttachStdout: true,
			AttachStderr: true,
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
	defer func() {
		_, _ = c.internal.ContainerRemove(context.Background(), resp.ID, client.ContainerRemoveOptions{
			Force: true,
		})
	}()

	if _, startErr := c.internal.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); startErr != nil {
		return fmt.Errorf("start container: %w", startErr)
	}

	var logBuf bytes.Buffer

	rc, err := c.internal.ContainerLogs(ctx, resp.ID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err == nil {
		if err := demuxDockerStream(rc, io.MultiWriter(os.Stdout, &logBuf)); err != nil {
			return fmt.Errorf("read container logs: %w", err)
		}
	}

	waitResult := c.internal.ContainerWait(ctx, resp.ID, client.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case chErr := <-waitResult.Error:
		if chErr != nil {
			return fmt.Errorf("wait container: %w", chErr)
		}
	case status := <-waitResult.Result:
		if status.StatusCode != 0 {
			if logBuf.Len() > 0 {
				return fmt.Errorf("%w: %d\n%s", errContainerExit, status.StatusCode, logBuf.String())
			}
			return fmt.Errorf("%w: %d", errContainerExit, status.StatusCode)
		}
	}

	return nil
}

// parseDockerStream reads a Docker daemon JSON stream (build or push),
// copies informational output to w, and returns any embedded error found.
func parseDockerStream(r io.Reader, w io.Writer) error {
	var collected []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg map[string]json.RawMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// Non-JSON lines (e.g. raw output from old Docker versions) pass through.
			fmt.Fprintln(w, string(line))
			continue
		}

		if errRaw, ok := msg["error"]; ok {
			var errStr string
			if json.Unmarshal(errRaw, &errStr) == nil && errStr != "" {
				collected = append(collected, errStr)
			} else {
				collected = append(collected, string(errRaw))
			}
		}
		if detailRaw, ok := msg["errorDetail"]; ok {
			var detail struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			if json.Unmarshal(detailRaw, &detail) == nil && detail.Message != "" {
				collected = append(collected, fmt.Sprintf("[code %d] %s", detail.Code, detail.Message))
			}
		}

		// Echo stream/status/id/progress to output.
		if stream, ok := msg["stream"]; ok {
			var s string
			if json.Unmarshal(stream, &s) == nil {
				fmt.Fprint(w, s)
			}
			continue
		}

		// Format push output: "id: status [progress]"
		var parts []string
		if idRaw, ok := msg["id"]; ok {
			var id string
			if json.Unmarshal(idRaw, &id) == nil && id != "" {
				parts = append(parts, id+":")
			}
		}
		if statusRaw, ok := msg["status"]; ok {
			var st string
			if json.Unmarshal(statusRaw, &st) == nil && st != "" {
				parts = append(parts, st)
			}
		}
		if progressRaw, ok := msg["progress"]; ok {
			var prog string
			if json.Unmarshal(progressRaw, &prog) == nil && prog != "" {
				parts = append(parts, prog)
			}
		}
		if auxRaw, ok := msg["aux"]; ok {
			parts = append(parts, string(auxRaw))
		}

		if len(parts) > 0 {
			fmt.Fprintln(w, strings.Join(parts, " "))
		}
	}

	if err := scanner.Err(); err != nil {
		if len(collected) > 0 {
			return fmt.Errorf("%s (scan error: %w)", strings.Join(collected, "; "), err)
		}
		return fmt.Errorf("read docker stream: %w", err)
	}

	if len(collected) > 0 {
		return fmt.Errorf("%w: %s", errDockerStream, strings.Join(collected, "; "))
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
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
	}
}
