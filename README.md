# coach — train your models, and don't overthink it

coach is a CLI tool for model training management.

coach is based on a primitive idea: your training artifact is a baby of the model and the data.

Usage:

```shell
coach run --model <model-image> --data <data-folder> --output <output-folder>
```

## Definitions

### Model

A model is a Docker image. You mount your data to /data folder and a path for the output to /output folder and run it.
It is **not** a daemon and must eventually finish; if it finished with a zero exit code, the output must exist afterward.

Image's digest serves as a unique fingerprint of the model version.

Optional: a model may also have /scripts folder. `coach list-scripts <model-image>` command will list all scripts in
that folder. You can run any of those scripts with `coach run-script <model-image> <script-name> [args...]` command.
Scripts are expected to be valid entrypoints, so they must be executable from inside the container.

Useful examples of scripts may be 'fetch-data', 'convert-artifact', 'evaluate', etc., but it's not specified.

### Data

Data is a set of files. Every file is considered a chunk. Every chunk is represented by its SHA256 checksum.

You can use .coachignore file to exclude some files from the data set (blacklisting), or .coachinclude file to only
include some files (whitelisting). If both files exist, only .coachinclude will be used.

### Artifact

An artifact is a baby of the model and the data. It can be literally anything; the only requirement is that the model
must write it to `/output`. Coach automatically maps `/output` to a host directory named after the artifact
fingerprint — a SHA256 hash of the model digest and the data chunks' hashes (sorted in ascending order).
So from the model's perspective, you simply write to `/output`; coach handles the `{fingerprint}/` subfolder on the host.

## Running with S3 data

`coach run` supports S3 data sources. When `--data` starts with `s3://`, Coach automatically builds a wrapper image
that pulls data from S3 into `/data`, runs the model, and copies `/output` back to a local directory.

```shell
coach run --model my-model:v1 --data s3://my-bucket/training-data/ --output ./output
```

S3 credentials come from `coach.json` (see below). The model image runs locally via Docker — no remote backend needed.

## Scheduling on remote backends

`coach schedule` submits training jobs to remote backends (Prefect, Scaleway, Vertex AI, etc.) via external driver executables.
Coach contains zero backend-specific code — each backend has its own driver that translates a standard JSON job spec
into the backend's API.

### Configuration (coach.json)

Create a `coach.json` in your project root or `~/.config/coach/`:

```json
{
  "backends": {
    "prefect": {
      "driver": "coach-prefect",
      "config": {
        "api_key": "$PREFECT_API_KEY",
        "workspace": "ml-training"
      }
    }
  },
  "defaultBackend": "prefect",
  "s3": {
    "accessKeyId": "$S3_ACCESS_KEY_ID",
    "secretAccessKey": "$S3_SECRET_ACCESS_KEY",
    "region": "us-east-1",
    "provider": "AWS"
  }
}
```

- `backends` — backend definitions. Each backend has:
  - `driver` — path or name of the driver executable (must be on `$PATH` or absolute)
  - `config` — arbitrary JSON passed to the driver via `COACH_BACKEND_CONFIG` env var
- `defaultBackend` — used when `--backend` is omitted
- `s3` — S3 credentials for both checksum resolution and container data sync:
  - `accessKeyId` — S3 access key ID
  - `secretAccessKey` — S3 secret access key
  - `region` — S3 region (default: auto-detected)
  - `endpoint` — custom endpoint for S3-compatible storage (optional)
  - `provider` — S3 provider name (AWS, Cloudflare, Minio, etc., default: AWS)
  - `s3` block is optional — if omitted, Coach falls back to the default AWS SDK credential chain

All values starting with `$` (like `$S3_ACCESS_KEY_ID`) are expanded from environment variables at load time.
This works for both `config` blocks and the `s3` block.

### Schedule commands

**Create a scheduled or one-off run:**

```shell
coach schedule create \
  --backend prefect \
  --model my-model:v1 \
  --data s3://my-bucket/training-data/ \
  --output s3://my-bucket/output/ \
  --schedule "0 */6 * * *" \
  --cpu 4 \
  --memory 16Gi \
  --label env=prod
```

Omit `--schedule` for a one-off run. `--data` accepts a single source (local path or `s3://` URI).
Data source and output must either both be local or both be S3 — mixing is not allowed.

**List scheduled runs:**

```shell
coach schedule list --backend prefect
```

Output is NDJSON (one JSON object per line).

**Check run status:**

```shell
coach schedule status <run-id> --backend prefect
```

**Delete a scheduled run:**

```shell
coach schedule delete <run-id> --backend prefect
```

### Container bridge — how S3 wrapping works

When `--data` starts with `s3://`, Coach automatically builds a **wrapper image** that bridges cloud storage with
the model container's expected `/data` and `/output` mount points.

This means drivers never need to understand S3, fetch data, or manage uploads — they just run a container.

#### How it works

1. Coach detects `s3://` in `--data`
2. Builds a wrapper Docker image on top of your model image:
   - Installs **rclone** inside the container
   - Adds an **entrypoint.sh** that handles all data movement
   - Entrypoint script preserves the original image's ENTRYPOINT so the model runs exactly as intended
3. Pushes the wrapper image to your registry (`COACH_REGISTRY` env var)
4. Forwards the wrapper image name + env vars (S3 paths and rclone credentials) to the driver
5. The driver creates a container from the wrapper image and injects the env vars — that's it

#### Container lifecycle

When the wrapper container starts, `entrypoint.sh` runs three phases:

1. **Phase 1: Pull** — `rclone copy s3-storage:$S3_PATH_IN /data` (downloads all data)
2. **Phase 2: Train** — runs your model's original entrypoint + cmd (reads `/data`, writes `/output`)
3. **Phase 3: Push** — `rclone copy /output s3-storage:$S3_PATH_OUT` (uploads results)

The rclone remote name is hardcoded to `s3-storage` inside the wrapper. Credentials come from the `s3` block
in `coach.json`, which are converted to `RCLONE_CONFIG_S3-STORAGE_*` env vars and injected into the container.

#### Requirements

- `COACH_REGISTRY` environment variable set to your container registry (e.g. `docker.io/myorg`)
- `s3` block in `coach.json` with S3 credentials
- Docker daemon accessible for building and pushing the wrapper image
- Your model image must be Debian/Ubuntu-based (wrapper installs rclone via `apt-get`)

#### Example

```shell
export COACH_REGISTRY=docker.io/myorg

coach schedule create \
  --backend prefect \
  --model my-model:v1 \
  --data s3://my-bucket/training-data/ \
  --output s3://my-bucket/output/ \
  --cpu 4 --memory 16Gi
```

With this `coach.json`:

```json
{
  "backends": { "prefect": { "driver": "coach-prefect", "config": {} } },
  "defaultBackend": "prefect",
  "s3": {
    "accessKeyId": "$S3_ACCESS_KEY_ID",
    "secretAccessKey": "$S3_SECRET_ACCESS_KEY",
    "region": "us-east-1",
    "provider": "AWS"
  }
}
```

Coach will build and push `docker.io/myorg/coach-wrapped-my-model-v1:abc123def456` before submitting the job.

The driver receives:

```json
{
  "model": {
    "image": "docker.io/myorg/coach-wrapped-my-model-v1:abc123def456",
    "envVars": {
      "S3_PATH_IN": "my-bucket/training-data/",
      "S3_PATH_OUT": "my-bucket/output/abc123def456",
      "RCLONE_CONFIG_S3-STORAGE_TYPE": "s3",
      "RCLONE_CONFIG_S3-STORAGE_PROVIDER": "AWS",
      "RCLONE_CONFIG_S3-STORAGE_REGION": "us-east-1",
      "RCLONE_CONFIG_S3-STORAGE_ACCESS_KEY_ID": "AKIA...",
      "RCLONE_CONFIG_S3-STORAGE_SECRET_ACCESS_KEY": "..."
    }
  }
}
```

The driver creates a container from this image, injects these env vars, and starts it. The entrypoint script handles everything else.

#### S3-compatible providers (Cloudflare R2, MinIO, etc.)

Set the `endpoint` and `provider` in the `s3` block of `coach.json`:

**Cloudflare R2:**
```json
{
  "s3": {
    "accessKeyId": "$R2_ACCESS_KEY_ID",
    "secretAccessKey": "$R2_SECRET_ACCESS_KEY",
    "endpoint": "https://<id>.r2.cloudflarestorage.com",
    "provider": "Cloudflare"
  }
}
```

**MinIO (self-hosted):**
```json
{
  "s3": {
    "accessKeyId": "minioadmin",
    "secretAccessKey": "minioadmin",
    "endpoint": "http://192.168.1.50:9000",
    "provider": "Minio"
  }
}
```

#### Without S3 (local data only)

If `--data` is a local path, Coach skips wrapping entirely and passes the original model image directly to the
driver — same behavior as before.

### How it works

1. Coach resolves checksums for the `--data` source (SHA256 for local files, S3 ETag for remote objects)
2. Computes an artifact fingerprint: `SHA256(model digest || sorted checksums)`
3. If the data source is an S3 URI: builds and pushes a wrapper image with rclone, derives `S3_PATH_IN`/`S3_PATH_OUT`
   from your URIs, converts `coach.json` `s3` block to `RCLONE_CONFIG_*` env vars
4. Builds a JSON job spec with the fingerprint, model (original or wrapped), data source, resources, env vars, and labels
5. Invokes the driver executable, passing the job spec via stdin and backend config via `COACH_BACKEND_CONFIG`
6. The driver translates the spec into the backend's API and returns the result as JSON on stdout

### Drivers

A driver is an executable that reads a JSON job spec from stdin and writes a JSON result to stdout.
Drivers can be written in any language.

#### How drivers work

The `driver` field in `coach.json` is resolved like a shell command:

- **Name only** (e.g. `"coach-prefect"`) — looked up on `$PATH`. Put your driver in `/usr/local/bin/`, `~/.local/bin/`, or any directory already in your `$PATH`.
- **Relative path** (e.g. `"./drivers/coach-prefect"`) — resolved relative to the current working directory.
- **Absolute path** (e.g. `"/home/user/projects/coach-drivers/coach-prefect"`) — used as-is.

A common layout is to keep drivers alongside your project:

```
my-project/
├── coach.json          # driver: "./drivers/coach-prefect"
├── drivers/
│   └── coach-prefect   # executable script or binary
├── data/
└── model.py
```

Coach executes the driver as a subprocess:

1. Writes the JSON job spec to the driver's **stdin**
2. Sets `COACH_BACKEND_CONFIG` environment variable with the backend's `config` block from `coach.json`
3. Waits for the driver to exit
4. Reads the JSON result from the driver's **stdout**
5. Captures **stderr** and displays it on failure

#### What a driver must implement

A driver must handle these operations:

| Operation | What to do |
|-----------|------------|
| `schedule` / `run` | Create a container from `model.image`, inject all keys from `model.envVars` as container env vars. Data/output mount paths are informational — the wrapper handles S3 internally. Return `scheduledRunId`. |
| `list` | List all existing runs. Return `entries` array with `id`, `schedule`, `status`, `url`. |
| `delete` | Delete a run by `scheduledRunId`. |
| `status` | Return the current status of a run by `scheduledRunId`. Return `state`, `lastRunAt`, `nextRunAt`. |

Exit code 0 = success, non-zero = failure.

#### Driver contract

All inputs include `"operation"`. All outputs include `"success"`. On failure (`"success": false`), include `"error"`.

##### `schedule` / `run` — create a new run (recurring or one-off)

**Input:**

```json
{"operation":"schedule","job":{"fingerprint":"abc123","model":{"image":"docker.io/myorg/coach-wrapped-my-model-v1:abc123def456","envVars":{"S3_PATH_IN":"bucket/data","S3_PATH_OUT":"bucket/output/abc123def456","RCLONE_CONFIG_S3-STORAGE_TYPE":"s3","RCLONE_CONFIG_S3-STORAGE_PROVIDER":"AWS","RCLONE_CONFIG_S3-STORAGE_REGION":"us-east-1","RCLONE_CONFIG_S3-STORAGE_ACCESS_KEY_ID":"AKIA...","RCLONE_CONFIG_S3-STORAGE_SECRET_ACCESS_KEY":"..."}},"data":{"sources":["s3://bucket/data/"],"mountPath":"/data"},"output":{"destination":"s3://bucket/output/","mountPath":"/output"},"resources":{"cpu":"4","memory":"16Gi"},"schedule":{"cron":"0 */6 * * *","timezone":"UTC"},"labels":{"env":"prod"}}}
```

`schedule` includes `job.schedule`; `run` does not. Optional fields: `job.name`, `job.model.command`, `job.model.script`, `job.resources.gpu`, `job.resources.gpuType`, `job.model.envVars`.

`model.envVars` is the general-purpose env var injection mechanism — all keys must be passed to the container as-is. For S3 runs it contains rclone credentials and S3 paths; drivers don't need to interpret them.

**Output:**

```json
{"success":true,"scheduledRunId":"run-abc-123","url":"https://cloud.prefect.io/..."}
```

##### `list` — list all scheduled/one-off runs

**Input:**

```json
{"operation":"list"}
```

**Output:**

```json
{"success":true,"entries":[{"id":"abc","schedule":"0 */6 * * *","status":"active","url":"https://..."}]}
```

`schedule` is omitted for one-off runs.

##### `delete` — delete a run by ID

**Input:**

```json
{"operation":"delete","scheduledRunId":"run-abc-123"}
```

**Output:**

```json
{"success":true}
```

##### `status` — get status of a single run

**Input:**

```json
{"operation":"status","scheduledRunId":"run-abc-123"}
```

**Output:**

```json
{"success":true,"status":{"id":"run-abc-123","state":"running","lastRunAt":"2026-05-01T10:00:00Z","nextRunAt":"2026-05-01T16:00:00Z"}}
```

**Exit code:** 0 = success, non-zero = failure.
