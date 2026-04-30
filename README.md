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

### Data

Data is a set of files. Every file is considered a chunk. Every chunk is represented by its SHA256 checksum.

You can use .coachignore file to exclude some files from the data set (blacklisting), or .coachinclude file to only
include some files (whitelisting). If both files exist, the whitelisting takes precedence over blacklisting.

### Artifact

An artifact is a baby of the model and the data. It can be literally anything; the only requirement is that it must
be created at /output/{fingerprint}/ folder, where the fingerprint is a SHA256 hash of the model digest and the data
chunks' hashes (sorted in ascending order).
