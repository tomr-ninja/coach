#!/usr/bin/env python3
import asyncio
import io
import json
import os
import sys
import traceback

from uuid import UUID

from prefect.client.orchestration import get_client
from prefect.client.schemas.actions import DeploymentScheduleCreate
from prefect.client.schemas.objects import Flow
from prefect.client.schemas.schedules import CronSchedule

PROTOCOL_VERSION = 1

VERBOSE = os.environ.get("COACH_VERBOSE") == "1"

config_raw = os.environ.get("COACH_BACKEND_CONFIG", "{}")
config = json.loads(config_raw)

api_url = config.get("api_url") or os.environ.get("PREFECT_API_URL", "")
if api_url:
    os.environ["PREFECT_API_URL"] = api_url


async def get_or_create_flow(client, flow_name):
    flows = await client.read_flows(limit=200)
    flows = [f for f in flows if f.name == flow_name]
    if flows:
        return flows[0]
    flow = Flow(name=flow_name)
    flow_id = await client.create_flow(flow)
    return Flow(id=flow_id, name=flow_name)


def _build_deployment_params(job, flow_id):
    model = job["model"]
    labels = job.get("labels", {})

    params = {
        "flow_id": flow_id,
        "name": job["name"],
        "tags": list(labels.values()) if labels else [],
        "parameters": {
            "fingerprint": job["fingerprint"],
            "model": model,
            "data": job["data"],
            "output": job["output"],
            "resources": job["resources"],
            "script": model.get("script", ""),
            "command": model.get("command", []),
        },
        "description": json.dumps(job),
        "job_variables": {
            "image": model["image"],
            "env": model.get("envVars", {}),
        },
        "work_pool_name": "coach-pool",
        "entrypoint": "container_runner:run_container",
        "path": "/flows",
    }

    sched = job.get("schedule")
    if sched:
        cron = CronSchedule(cron=sched["cron"], timezone=sched.get("timezone", "UTC"))
        params["schedules"] = [DeploymentScheduleCreate(schedule=cron, active=True)]

    return params


async def submit_job(client, job):
    if not job.get("isWrapped", False):
        return {"success": False, "error": "prefect driver requires wrapped images (S3 data); local data paths are not supported"}

    flow_name = job.get("name", "coach-container-runner")
    flow = await get_or_create_flow(client, flow_name)
    params = _build_deployment_params(job, flow.id)
    deployment_id = await client.create_deployment(**params)

    # create_deployment returns a UUID in newer Prefect versions.
    # Convert to plain string safely regardless of return type.
    if isinstance(deployment_id, UUID):
        deployment_id_str = str(deployment_id)
    elif isinstance(deployment_id, str):
        deployment_id_str = deployment_id
    else:
        raise TypeError(
            f"create_deployment returned unexpected type {type(deployment_id).__name__}, "
            f"expected UUID or str. Value: {deployment_id!r}"
        )

    if not job.get("isRecurring", False):
        # Prefect's create_flow_run_from_deployment accepts UUID or str.
        await client.create_flow_run_from_deployment(deployment_id)

    return deployment_id_str


async def list_deployments(client):
    deployments = await client.read_deployments()
    entries = []
    for d in deployments:
        cron = ""
        if d.schedules:
            schedule_obj = d.schedules[0]
            if isinstance(schedule_obj, dict):
                cron = schedule_obj.get("cron", "")
            elif hasattr(schedule_obj, "schedule") and schedule_obj.schedule:
                cron = schedule_obj.schedule.cron
        entries.append(
            {
                "id": str(d.id),
                "schedule": cron,
                "status": "paused" if d.paused else "active",
                "url": "",
            }
        )
    return entries


async def delete_deployment(client, run_id):
    await client.delete_deployment(UUID(str(run_id)))


async def deployment_status(client, run_id):
    deployment = await client.read_deployment(UUID(str(run_id)))
    next_run = ""
    if deployment.schedules:
        schedule_obj = deployment.schedules[0]
        if hasattr(schedule_obj, "schedule") and schedule_obj.schedule:
            try:
                sched = schedule_obj.schedule
                if isinstance(sched, dict):
                    pass
                elif hasattr(sched, "upcoming"):
                    upcoming = sched.upcoming(n=1)
                    if upcoming:
                        next_run = upcoming[0].isoformat()
            except Exception:
                pass

    return {
        "id": str(deployment.id),
        "state": "paused" if deployment.paused else "active",
        "lastRunAt": "",
        "nextRunAt": next_run,
    }


def write_result(result):
    result["protocolVersion"] = PROTOCOL_VERSION
    print(json.dumps(result))
    sys.exit(0)


def run():
    try:
        spec = json.load(io.BytesIO(sys.stdin.buffer.read(10_000_000)))
    except json.JSONDecodeError as e:
        write_result({"success": False, "error": f"invalid input json: {e}"})

    if VERBOSE:
        print(f"[prefect driver] spec: {json.dumps(spec)}", file=sys.stderr)
    spec_type = spec.get("type")
    job = spec.get("job", {})
    run_id = spec.get("id")

    async def execute():
        import time as _time
        _start = _time.monotonic()
        if VERBOSE:
            print(f"[prefect driver] executing {spec_type}...", file=sys.stderr)
        async with get_client() as client:
            if spec_type == "submit":
                sid = await submit_job(client, job)
                return {"success": True, "submitResult": {"id": sid}}
            elif spec_type == "list":
                entries = await list_deployments(client)
                return {"success": True, "listResult": {"entries": entries}}
            elif spec_type == "delete":
                await delete_deployment(client, run_id)
                return {"success": True}
            elif spec_type == "status":
                st = await deployment_status(client, run_id)
                return {"success": True, "statusResult": st}
            else:
                return {"success": False, "error": f"unknown type: {spec_type}"}
        return None

    try:
        result = asyncio.run(execute())
        if VERBOSE:
            elapsed = time.monotonic() - _start
            print(f"[prefect driver] {spec_type} completed in {elapsed:.2f}s", file=sys.stderr)
    except Exception as e:
        traceback.print_exc(file=sys.stderr)
        result = {"success": False, "error": str(e)}

    write_result(result)


if __name__ == "__main__":
    run()