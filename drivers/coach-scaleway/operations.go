package main

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/tomr-ninja/coach/protocol"
)

func submit(api *ScalewayAPI, job *protocol.Job) *protocol.DriverResult {
	if !job.IsWrapped {
		return &protocol.DriverResult{Success: false, Error: "scaleway driver requires wrapped images (S3 data); local data paths are not supported"}
	}

	jd, err := buildJobDefinition(job, api.Project)
	if err != nil {
		return &protocol.DriverResult{Success: false, Error: fmt.Errorf("build job definition: %w", err).Error()}
	}
	sid, err := api.createJobDefinition(jd)
	if err != nil {
		return &protocol.DriverResult{Success: false, Error: fmt.Errorf("create job definition: %w", err).Error()}
	}

	if !job.IsRecurring {
		if err := api.startJobDefinition(sid); err != nil {
			return &protocol.DriverResult{Success: false, Error: fmt.Errorf("start job: %w", err).Error()}
		}
	}

	return &protocol.DriverResult{
		Success: true,
		SubmitResult: &protocol.SubmitResult{
			ID:  sid,
			URL: consoleURL(api.Region, sid),
		},
	}
}

func listResult(api *ScalewayAPI) *protocol.DriverResult {
	defs, err := api.listJobDefinitions()
	if err != nil {
		return &protocol.DriverResult{Success: false, Error: fmt.Errorf("list job definitions: %w", err).Error()}
	}

	var entries []protocol.ScheduleEntry
	for _, d := range defs {
		schedule := ""
		status := "active"
		if d.CronSchedule != nil {
			schedule = d.CronSchedule.Schedule
		}
		if d.Status != "" {
			status = d.Status
		}

		entries = append(entries, protocol.ScheduleEntry{
			ID:       d.ID,
			Schedule: schedule,
			Status:   status,
			URL:      consoleURL(api.Region, d.ID),
		})
	}

	return &protocol.DriverResult{
		Success: true,
		ListResult: &protocol.ListResult{
			Entries: entries,
		},
	}
}

func deleteResult(api *ScalewayAPI, id string) *protocol.DriverResult {
	if err := api.deleteJobDefinition(id); err != nil {
		return &protocol.DriverResult{Success: false, Error: fmt.Errorf("delete job definition: %w", err).Error()}
	}
	return &protocol.DriverResult{Success: true}
}

func statusResult(api *ScalewayAPI, id string) *protocol.DriverResult {
	jd, err := api.getJobDefinition(id)
	if err != nil {
		return &protocol.DriverResult{Success: false, Error: fmt.Errorf("get job definition: %w", err).Error()}
	}

	var lastRunAt string
	latest, err := api.latestJobRun(id)
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

	return &protocol.DriverResult{
		Success: true,
		StatusResult: &protocol.StatusResult{
			ID:        jd.ID,
			State:     state,
			LastRunAt: lastRunAt,
			NextRunAt: nextRunAt,
		},
	}
}

func consoleURL(region, id string) string {
	return "https://console.scaleway.com/serverless-jobs/jobs/" + region + "/" + id + "/overview"
}
