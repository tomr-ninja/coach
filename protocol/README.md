# Driver Protocol

The driver protocol is the interface between coach core and backend-specific driver executables.
It is versioned so breaking changes can be introduced explicitly.

**Current protocol version: 1**

The canonical type definitions live in `types.go`. The protocol version constant is in `types.go:3`.

## Architecture

Coach contains zero backend-specific code. Each backend (Prefect, Scaleway, Vertex AI, etc.) has its own driver — an
executable in any language — that translates a standard JSON spec into the backend's API.

Coach invokes the driver as a subprocess:

1. Writes the JSON spec to the driver's **stdin**
2. Sets `COACH_BACKEND_CONFIG` environment variable with the backend's `config` block from `coach.json`
3. Waits for the driver to exit (timeout: 5 minutes)
4. Reads the JSON result from the driver's **stdout** (max 10 MB)
5. Captures **stderr** and displays it on failure

## Driver resolution

The `driver` field in `coach.json` is resolved like a shell command:

- **Name only** (e.g. `"coach-prefect"`) — looked up on `$PATH`
- **Relative path** (e.g. `"./drivers/coach-prefect"`) — resolved relative to CWD
- **Absolute path** (e.g. `"/usr/local/bin/coach-prefect"`) — used as-is

## Protocol version

All specs include `"protocolVersion": 1` and all results must include `"protocolVersion": 1`.

Coach validates that the driver's reported version matches its own exactly. A version mismatch is a hard error.

## Exit code contract

- **Exit code 0** — driver ran to completion. Check `success` in the JSON result.
- **Exit code non-zero** — driver crashed or hit an internal bug. Coach treats this as a hard failure (no result parsed).

Operational errors (e.g. "job not found", "API rate limit") must return exit code 0 with `"success": false` and an `"error"` message. Only crashes/bugs use non-zero exit codes.

## Spec types

A driver must handle these four spec types:

| Type     | What to do                                                                                                                                                                                                                                                                |
|----------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `submit` | Create a container from `job.model.image`, inject all keys from `job.model.envVars` as container env vars. If `job.isRecurring` is true, set up recurring execution per `job.schedule`. If false, run once immediately. Return `submitResult` with the run/deployment ID. |
| `list`   | List all existing runs. Return `listResult` with `entries` array.                                                                                                                                                                                                         |
| `delete` | Delete a run by `id`.                                                                                                                                                                                                                                                     |
| `status` | Return the current status of a run by `id`. Return `statusResult`.                                                                                                                                                                                                        |

All inputs include `protocolVersion` and `type`. All outputs include `success` and `protocolVersion`. On failure (`"success": false`), include `"error"`.

---

## Type reference

### Spec (input to driver)

```go
type Spec struct {
    ProtocolVersion int    `json:"protocolVersion"`
    Type            string `json:"type"`           // "submit" | "list" | "delete" | "status"
    ID              string `json:"id,omitempty"`   // present for delete, status
    Job             *Job   `json:"job,omitempty"`  // present for submit
}
```

### Job

```go
type Job struct {
    Fingerprint string            `json:"fingerprint"`           // artifact fingerprint hex
    Name        string            `json:"name"`                  // e.g. "coach-container-runner-my-model-v1"
    IsWrapped   bool              `json:"isWrapped"`             // image includes S3 wrapper entrypoint
    IsRecurring bool              `json:"isRecurring"`           // true for scheduled, false for one-off
    Schedule    *Schedule         `json:"schedule,omitempty"`    // present when isRecurring=true
    Model       Model             `json:"model"`
    Data        Data              `json:"data"`
    Output      Output            `json:"output"`
    Resources   Resources         `json:"resources"`
    Labels      map[string]string `json:"labels,omitempty"`     // user-supplied key=value labels
}
```

### Schedule

```go
type Schedule struct {
    Cron     string `json:"cron"`
    Timezone string `json:"timezone,omitempty"`
}
```

### Model

```go
type Model struct {
    Image   string            `json:"image"`             // Docker image to run
    Command []string          `json:"command,omitempty"` // override container CMD
    Script  string            `json:"script,omitempty"`  // script name from /scripts/ folder
    EnvVars map[string]string `json:"envVars,omitempty"` // env vars to inject into container
}
```

**`script` semantics:** When `script` is set, drivers should run `/scripts/<script>` inside the container with
`command` as args (if present). When only `command` is set, it overrides the container CMD. When both are set,
the script path is the entrypoint and command provides arguments. These are hints — drivers may interpret or ignore
them based on backend capabilities.

### Data

```go
type Data struct {
    Sources   []string `json:"sources"`   // data source URIs
    MountPath string   `json:"mountPath"` // mount path inside container
}
```

`Data` is informational. When `isWrapped` is true, the wrapper entrypoint handles data I/O and drivers must **not**
attempt volume mounts or S3 transfers. When `isWrapped` is false, drivers should mount data at `mountPath` where
possible.

### Output

```go
type Output struct {
    Destination string `json:"destination"` // output destination URI
    MountPath   string `json:"mountPath"`   // mount path inside container
}
```

Same semantics as Data: informational when wrapped, mount target when not wrapped.

### Resources

```go
type Resources struct {
    CPUMillicores uint32 `json:"cpuMillicores,omitempty"`
    MemoryMi      uint32 `json:"memoryMi,omitempty"`
    GPU           uint32 `json:"gpu,omitempty"`
    GPUType       string `json:"gpuType,omitempty"`
}
```

Resource parsing utilities (`ParseCPU`, `ParseMemory`, `ParseGPU`, `ParseResources`) are in `resources.go`.

### DriverResult (output from driver)

```go
type DriverResult struct {
    Success         bool          `json:"success"`
    Error           string        `json:"error,omitempty"`
    ProtocolVersion int           `json:"protocolVersion"`
    SubmitResult    *SubmitResult `json:"submitResult,omitempty"`
    ListResult      *ListResult   `json:"listResult,omitempty"`
    StatusResult    *StatusResult `json:"statusResult,omitempty"`
}
```

## Operation examples

### submit — create a new run

**Input (one-off):**

```json
{
  "protocolVersion": 1,
  "type": "submit",
  "job": {
    "fingerprint": "abc123",
    "name": "coach-container-runner-my-model-v1",
    "isRecurring": false,
    "isWrapped": true,
    "model": {
      "image": "docker.io/myorg/coach-wrapped-my-model-v1:abc123def456",
      "envVars": {
        "S3_PATH_IN": "bucket/data",
        "S3_PATH_OUT": "bucket/output/abc123def456",
        "RCLONE_CONFIG_S3-STORAGE_TYPE": "s3",
        "RCLONE_CONFIG_S3-STORAGE_PROVIDER": "AWS",
        "RCLONE_CONFIG_S3-STORAGE_REGION": "us-east-1",
        "RCLONE_CONFIG_S3-STORAGE_ACCESS_KEY_ID": "AKIA...",
        "RCLONE_CONFIG_S3-STORAGE_SECRET_ACCESS_KEY": "..."
      }
    },
    "data": {
      "sources": ["s3://bucket/data/"],
      "mountPath": "/data"
    },
    "output": {
      "destination": "s3://bucket/output/",
      "mountPath": "/output"
    },
    "resources": {
      "cpuMillicores": 4000,
      "memoryMi": 16384
    },
    "labels": {
      "env": "prod"
    }
  }
}
```

**Input (recurring):**

Same as above but with `"isRecurring": true` and `"schedule": {"cron": "0 */6 * * *", "timezone": "UTC"}`.

**Output:**

```json
{
  "success": true,
  "protocolVersion": 1,
  "submitResult": {
    "id": "run-abc-123",
    "url": "https://console.example.com/runs/run-abc-123"
  }
}
```

### list — list all runs

**Input:**

```json
{"protocolVersion": 1, "type": "list"}
```

**Output:**

```json
{
  "success": true,
  "protocolVersion": 1,
  "listResult": {
    "entries": [
      {
        "id": "abc",
        "schedule": "0 */6 * * *",
        "status": "active",
        "url": "https://console.example.com/runs/abc"
      }
    ]
  }
}
```

`schedule` is omitted for one-off runs. `entries` may be empty.

### delete — delete a run

**Input:**

```json
{"protocolVersion": 1, "type": "delete", "id": "run-abc-123"}
```

**Output:**

```json
{"success": true, "protocolVersion": 1}
```

### status — get run status

**Input:**

```json
{"protocolVersion": 1, "type": "status", "id": "run-abc-123"}
```

**Output:**

```json
{
  "success": true,
  "protocolVersion": 1,
  "statusResult": {
    "id": "run-abc-123",
    "state": "running",
    "lastRunAt": "2026-05-01T10:00:00Z",
    "nextRunAt": "2026-05-01T16:00:00Z"
  }
}
```

## Wrapped vs. unwrapped jobs

`job.isWrapped` indicates whether coach built a wrapper image around the model:

- **`isWrapped: true`** — The image includes an rclone-based entrypoint that handles S3 data pull and output push.
Drivers must pass `model.envVars` to the container but must **not** attempt volume mounts or S3 transfers. Data is
available at `/data` inside the container; output is written to `/output`.
- **`isWrapped: false`** — The image is the raw model image. Drivers should mount data at `data.mountPath` and output
at `output.mountPath` where the backend supports it. `model.envVars` is still injected if present.
