# Coach — train your models; don't overthink it

Coach is a CLI tool for model training management. It does three main things for you:

1. Artifact versioning — automatically computes a unique fingerprint for every combination of model and data.
2. Remote scheduling — submit training jobs to any backend (Prefect, Scaleway, Vertex AI, etc.) using drivers.
3. S3 support — seamlessly use S3 buckets as data sources and output targets as if it was local storage.

Your model may remain completely oblivious about any of that happening; your backend doesn't need any additional setup*.

\* *Some backends, like Prefect, require a bit of setup just to enable running Docker containers.*

## Command tree

```
coach [--verbose]
├── cleanup [-dry-run] [-older <duration>]
├── run [-data] [-output] [-force] <model-image>
├── script
│   ├── ls <model-image>
│   └── run [-data] [-output] <model-image> <script> [args...]
└── schedule [-backend]
    ├── create [-data] [-output] [-schedule] [-command]... [-script]
    │          [-cpu] [-memory] [-gpu] [-gpu-type] [-label]... <model-image>
    ├── ls
    ├── delete <id>
    └── status <id>
```

## Definitions

### Model

A model is a Docker image. You mount your data to /data folder and a path for the output to /output folder and run it.
It is **not** a daemon and must eventually finish; if it finished with a zero exit code, the output must exist afterward.

Image's digest serves as a unique fingerprint of the model version.

Optional: a model may also have /scripts folder. `coach script ls <model-image>` lists all scripts in
that folder. You can run any of those scripts with `coach script run <model-image> <script-name> [args...]`.
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

## Local usage

### Running a model

```shell
coach run [-data <data-folder>] [-output <output-folder>] [-force] <model-image>
```

`-data` and `-output` default to `./data` and `./output` if omitted. `<model-image>` is required.

You can also pass S3 URIs for `-data` and `-output` to run locally with S3-backed data:

```shell
coach run --data s3://my-bucket/training-data/ --output s3://my-bucket/output/ my-model:v1
```

When S3 URIs are used, Coach builds and runs a wrapper container (same as described
in the [Container bridge](#container-bridge--how-s3-wrapping-works) section), pulling data from S3 before training
and pushing results back after.

### Cleanup

Coach builds wrapper Docker images for S3-backed runs. Over time these accumulate and consume
disk space. `coach cleanup` removes stale wrapper images.

```shell
coach cleanup [-dry-run] [-older <duration>]
```

- `-dry-run` — Print what would be removed without actually deleting.
- `-older` — Only remove images older than the given duration (default: `24h`). Accepts Go-style
duration strings: `24h`, `7d`, `30m`, etc. Set to `0` to remove all wrapper images regardless of age.

Example:

```shell
coach cleanup --older 7d
```

## Scheduling on remote backends

`coach schedule` submits training jobs to remote backends (Prefect, Scaleway, Vertex AI, etc.) via external driver executables.
Coach contains zero backend-specific code — each backend has its own driver that translates a standard JSON job spec
into the backend's API.

### Configuration (coach.json)

Create a `coach.json` in your project root or `~/.config/coach/`:

```json
{
  "registry": "docker.io/myorg",
  "registryAuth": "$DOCKER_AUTH",
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

- `registry` — container registry for pushing wrapped images (e.g. `docker.io/myorg`). If omitted, the image is built but not pushed.
- `registryAuth` — registry auth string (format: `username:password`). Only needed for remote backends.
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

### Schedule commands

**Create a scheduled or one-off run:**

```shell
coach schedule --backend prefect create \
  --data s3://my-bucket/training-data/ \
  --output s3://my-bucket/output/ \
  --schedule "0 */6 * * *" \
  --cpu 4 \
  --memory 16Gi \
  --gpu 1 \
  --gpu-type T4 \
  --label env=prod \
  my-model:v1
```

`-backend` goes on the `schedule` command itself, before the subcommand. Omit `-schedule` for a one-off run.
`-data` accepts a single source (local path or `s3://` URI).
Data source and output must either both be local or both be S3 — mixing is not allowed.
`-script` runs a named script from `/scripts/` in the container; `-command` passes additional command args
(e.g. `-command python -command -u -command train.py`). The model image is a positional argument.

**List scheduled runs:**

```shell
coach schedule --backend prefect ls
```

Output is NDJSON (one JSON object per line).

**Check run status:**

```shell
coach schedule --backend prefect status <run-id>
```

**Delete a scheduled run:**

```shell
coach schedule --backend prefect delete <run-id>
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
3. Pushes the wrapper image to your registry (`registry` field in `coach.json`)
4. Forwards the wrapper image name + env vars (S3 paths and rclone credentials) to the driver
5. The driver creates a container from the wrapper image and injects the env vars — that's it

#### Container lifecycle

When the wrapper container starts, `entrypoint.sh` runs three phases:

1. **Phase 1: Pull** — `rclone copy s3:$S3_PATH_IN /data` (downloads all data)
2. **Phase 2: Train** — runs your model's original entrypoint + cmd (reads `/data`, writes `/output`)
3. **Phase 3: Push** — `rclone copy /output s3:$S3_PATH_OUT` (uploads results)

The rclone remote name is hardcoded to `s3` inside the wrapper. Credentials come from the `s3` block
in `coach.json`, which are converted to `RCLONE_CONFIG_S3_*` env vars and injected into the container.

#### Requirements

- `registry` field set in `coach.json` with your container registry (e.g. `docker.io/myorg`)
- `registryAuth` in `coach.json` with registry credentials in `username:password` format (optional, only for private registries)
- `s3` block in `coach.json` with S3 credentials
- Docker daemon accessible for building and pushing the wrapper image

#### Example

With this `coach.json`:

```json
{
  "backends": { "prefect": { "driver": "coach-prefect", "config": {} } },
  "defaultBackend": "prefect",
  "registry": "docker.io/myorg",
  "s3": {
    "accessKeyId": "$S3_ACCESS_KEY_ID",
    "secretAccessKey": "$S3_SECRET_ACCESS_KEY",
    "region": "us-east-1",
    "provider": "AWS"
  }
}
```

```shell
coach schedule --backend prefect create \
  --data s3://my-bucket/training-data/ \
  --output s3://my-bucket/output/ \
  --cpu 4 --memory 16Gi \
  my-model:v1
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
      "RCLONE_CONFIG_S3_TYPE": "s3",
      "RCLONE_CONFIG_S3_PROVIDER": "AWS",
      "RCLONE_CONFIG_S3_REGION": "us-east-1",
      "RCLONE_CONFIG_S3_ACCESS_KEY_ID": "AKIA...",
      "RCLONE_CONFIG_S3_SECRET_ACCESS_KEY": "..."
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

1. Coach resolves checksums for the `--data` source (SHA256 for local files, SHA256 of S3 ETag for remote objects)
2. Computes an artifact fingerprint: `SHA256(model digest || sorted checksums)`
3. If the data source is an S3 URI: builds and pushes a wrapper image with rclone, derives `S3_PATH_IN`/`S3_PATH_OUT`
   from your URIs, converts `coach.json` `s3` block to `RCLONE_CONFIG_*` env vars
4. Builds a JSON job spec with the fingerprint, model (original or wrapped), data source, resources, env vars, and labels
5. Invokes the driver executable, passing the job spec via stdin and backend config via `COACH_BACKEND_CONFIG`
6. The driver translates the spec into the backend's API and returns the result as JSON on stdout

### Drivers

Drivers are external executables that translate a standard JSON job spec into backend-specific API calls.
Drivers can be written in any language. Three reference implementations are included:

- `drivers/coach-prefect/` — Python driver for Prefect
- `drivers/coach-scaleway/` — Go driver for Scaleway Serverless Jobs
- `drivers/coach-nebius/` — Go driver for Nebius Cloud

The full driver protocol specification, including the JSON contract, exit code rules, and operation examples, is
in **[protocol/README.md](protocol/README.md)**.
