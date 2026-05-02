package main

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

func run(api *ScalewayAPI, spec *JobSpec) (map[string]any, error) {
	jd := buildJobDefinition(spec.Job, api.Project)
	sid, err := api.createJobDefinition(jd)
	if err != nil {
		return nil, fmt.Errorf("create job definition: %w", err)
	}

	if err := api.startJobDefinition(sid); err != nil {
		return nil, fmt.Errorf("start job: %w", err)
	}

	return map[string]any{
		"success":        true,
		"scheduledRunId": sid,
		"url":            consoleURL(api.Region, sid),
	}, nil
}

func schedule(api *ScalewayAPI, spec *JobSpec) (map[string]any, error) {
	jd := buildJobDefinition(spec.Job, api.Project)
	sid, err := api.createJobDefinition(jd)
	if err != nil {
		return nil, fmt.Errorf("create job definition: %w", err)
	}

	return map[string]any{
		"success":        true,
		"scheduledRunId": sid,
		"url":            consoleURL(api.Region, sid),
	}, nil
}

func list(api *ScalewayAPI) (map[string]any, error) {
	defs, err := api.listJobDefinitions()
	if err != nil {
		return nil, fmt.Errorf("list job definitions: %w", err)
	}

	var entries []map[string]any
	for _, d := range defs {
		schedule := ""
		status := "active"
		if d.CronSchedule != nil {
			schedule = d.CronSchedule.Schedule
		}
		if d.Status != "" {
			status = d.Status
		}

		entries = append(entries, map[string]any{
			"id":       d.ID,
			"schedule": schedule,
			"status":   status,
			"url":      consoleURL(api.Region, d.ID),
		})
	}

	return map[string]any{
		"success": true,
		"entries": entries,
	}, nil
}

func deleteOp(api *ScalewayAPI, runID string) (map[string]any, error) {
	if err := api.deleteJobDefinition(runID); err != nil {
		return nil, fmt.Errorf("delete job definition: %w", err)
	}
	return map[string]any{"success": true}, nil
}

func statusOp(api *ScalewayAPI, runID string) (map[string]any, error) {
	jd, err := api.getJobDefinition(runID)
	if err != nil {
		return nil, fmt.Errorf("get job definition: %w", err)
	}

	var lastRunAt string
	latest, err := api.latestJobRun(runID)
	if err == nil {
		lastRunAt = latest.CreatedAt.Format(time.RFC3339)
	}

	var nextRunAt string
	state := jd.Status
	if jd.CronSchedule != nil {
		s, err := cron.ParseStandard(jd.CronSchedule.Schedule)
		if err == nil {
			tz := time.UTC
			if jd.CronSchedule.Timezone != "" {
				if loc, err := time.LoadLocation(jd.CronSchedule.Timezone); err == nil {
					tz = loc
				}
			}
			nextRunAt = s.Next(time.Now().In(tz)).Format(time.RFC3339)
		}
		if state == "" {
			state = "active"
		}
	}

	return map[string]any{
		"success": true,
		"status": map[string]any{
			"id":        jd.ID,
			"state":     state,
			"lastRunAt": lastRunAt,
			"nextRunAt": nextRunAt,
		},
	}, nil
}

func consoleURL(region, id string) string {
	return "https://console.scaleway.com/serverless-jobs/jobs/" + region + "/" + id + "/overview"
}
