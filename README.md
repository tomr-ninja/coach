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

An artifact is a baby of the model and the data. It can be literally anything; the only requirement is that it must
be created at /output/{fingerprint}/ folder, where the fingerprint is a SHA256 hash of the model digest and the data
chunks' hashes (sorted in ascending order).
