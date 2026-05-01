#!/usr/bin/env python3
import asyncio
import json
import os
import sys

config_raw = os.environ.get("COACH_BACKEND_CONFIG", "{}")
config = json.loads(config_raw)

api_url = config.get("api_url") or os.environ.get("PREFECT_API_URL", "")
if api_url:
    os.environ["PREFECT_API_URL"] = api_url

from prefect.client.orchestration import get_client
from prefect.client.schemas.actions import DeploymentScheduleCreate
from prefect.client.schemas.objects import Flow
from prefect.client.schemas.schedules import CronSchedule

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


async def run_oneoff(client, job):
    flow_name = job.get("flowName", "coach-container-runner")
    flow = await get_or_create_flow(client, flow_name)
    params = _build_deployment_params(job, flow.id)
    deployment_id = await client.create_deployment(**params)
    await client.create_flow_run_from_deployment(deployment_id)
    return str(deployment_id)


async def schedule(client, job):
    flow_name = job.get("flowName", "coach-container-runner")
    flow = await get_or_create_flow(client, flow_name)
    params = _build_deployment_params(job, flow.id)
    deployment_id = await client.create_deployment(**params)
    return str(deployment_id)


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
    from uuid import UUID

    await client.delete_deployment(UUID(str(run_id)))


async def deployment_status(client, run_id):
    from uuid import UUID
    from datetime import timezone as _tz

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


def run():
    try:
        spec = json.load(sys.stdin)
    except json.JSONDecodeError as e:
        print(json.dumps({"success": False, "error": f"invalid input json: {e}"}))
        sys.exit(0)

    operation = spec.get("operation")
    job = spec.get("job", {})
    run_id = spec.get("scheduledRunId")

    async def execute():
        async with get_client() as client:
            if operation == "run":
                sid = await run_oneoff(client, job)
                return {"success": True, "scheduledRunId": sid}
            elif operation == "schedule":
                sid = await schedule(client, job)
                return {"success": True, "scheduledRunId": sid}
            elif operation == "list":
                entries = await list_deployments(client)
                return {"success": True, "entries": entries}
            elif operation == "delete":
                await delete_deployment(client, run_id)
                return {"success": True}
            elif operation == "status":
                st = await deployment_status(client, run_id)
                return {"success": True, "status": st}
            else:
                return {"success": False, "error": f"unknown operation: {operation}"}

    try:
        result = asyncio.run(execute())
    except Exception as e:
        result = {"success": False, "error": str(e)}

    print(json.dumps(result))
    sys.exit(0)


if __name__ == "__main__":
    run()
