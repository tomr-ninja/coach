import sys
import traceback

import docker
from docker.errors import ContainerError, DockerException
from prefect import flow


@flow(log_prints=True)
def run_container(
    imageDigest: str,
    model: dict,
    data: dict,
    output: dict,
    resources: dict,
    script: str = "",
    command: list = None,
):
    image = model["image"]
    env_vars = model.get("envVars", {})

    try:
        client = docker.from_env()
    except DockerException as e:
        print(f"Failed to connect to Docker daemon: {e}", file=sys.stderr)
        raise RuntimeError(f"docker daemon unavailable: {e}") from e

    print(f"Running: {image}")

    try:
        container = client.containers.run(
            image,
            command=command or None,
            environment=env_vars or None,
            remove=False,
            stdout=True,
            stderr=True,
            detach=True,
        )
    except ContainerError as e:
        print(f"Container failed: {e}", file=sys.stderr)
        if hasattr(e, "stderr") and e.stderr:
            print(e.stderr.decode() if isinstance(e.stderr, bytes) else e.stderr, file=sys.stderr)
        raise RuntimeError(f"container failed: {e}") from e
    except Exception as e:
        print(f"Failed to run container: {e}", file=sys.stderr)
        raise RuntimeError(f"container run error: {e}") from e

    exit_code = None

    try:
        result = container.wait()
        exit_code = result.get("StatusCode")

        logs = container.logs(stdout=True, stderr=True)
        if logs:
            text = logs.decode() if isinstance(logs, bytes) else logs
            print(text)
    except Exception as e:
        print(f"Failed to wait for container or fetch logs: {e}", file=sys.stderr)
        traceback.print_exc(file=sys.stderr)
    finally:
        try:
            container.remove(force=True)
        except Exception as e:
            print(f"Warning: failed to remove container {container.id}: {e}", file=sys.stderr)

    if exit_code is None:
        raise RuntimeError("container exited but exit code could not be determined")

    print(f"Container exited with code: {exit_code}")
    if exit_code != 0:
        raise RuntimeError(f"container failed with code {exit_code}")
