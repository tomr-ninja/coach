import docker
from prefect import flow


@flow(log_prints=True)
def run_container(
    fingerprint: str,
    model: dict,
    data: dict,
    output: dict,
    resources: dict,
    script: str = "",
    command: list = None,
):
    image = model["image"]
    env_vars = model.get("envVars", {})

    client = docker.from_env()
    print(f"Running: {image}")

    result = client.containers.run(
        image,
        command=command or None,
        environment=env_vars or None,
        remove=True,
        stdout=True,
        stderr=True,
    )

    exit_code = 0
    if isinstance(result, bytes):
        text = result.decode()
        if text:
            print(text)
    elif hasattr(result, "attrs"):
        exit_code = result.attrs["State"]["ExitCode"]

    print(f"Container exited with code: {exit_code}")
    if exit_code != 0:
        raise RuntimeError(f"container failed with code {exit_code}")
