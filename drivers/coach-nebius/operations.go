package main

import (
	"context"
	"fmt"
	"time"

	"github.com/tomr-ninja/coach/protocol"
)

const driverTimeout = 10 * time.Minute

func submit(ctx context.Context, api *NebiusAPI, job *protocol.Job) *protocol.DriverResult {
	if !job.IsWrapped {
		return &protocol.DriverResult{Success: false, Error: "nebius driver requires wrapped images (S3 data); local data paths are not supported"}
	}

	subCtx, cancel := context.WithTimeout(ctx, driverTimeout)
	defer cancel()

	id, err := api.CreateJob(subCtx, job)
	if err != nil {
		return &protocol.DriverResult{Success: false, Error: fmt.Sprintf("create job: %v", err)}
	}

	return &protocol.DriverResult{
		Success: true,
		SubmitResult: &protocol.SubmitResult{
			ID:  id,
			URL: consoleURL(id),
		},
	}
}

func listResult(ctx context.Context, api *NebiusAPI) *protocol.DriverResult {
	jobs, err := api.ListJobs(ctx)
	if err != nil {
		return &protocol.DriverResult{Success: false, Error: fmt.Sprintf("list jobs: %v", err)}
	}

	var entries []protocol.ScheduleEntry
	for _, job := range jobs {
		state := "unknown"
		if job.Status != nil {
			state = job.Status.GetState().String()
		}

		entries = append(entries, protocol.ScheduleEntry{
			ID:     job.GetMetadata().GetId(),
			Status: state,
			URL:    consoleURL(job.GetMetadata().GetId()),
		})
	}

	return &protocol.DriverResult{
		Success: true,
		ListResult: &protocol.ListResult{
			Entries: entries,
		},
	}
}

func deleteResult(ctx context.Context, api *NebiusAPI, id string) *protocol.DriverResult {
	// Cancel first if running, then delete.
	if err := api.CancelJob(ctx, id); err != nil {
		// Ignore cancel errors (job may already be stopped)
	}
	if err := api.DeleteJob(ctx, id); err != nil {
		return &protocol.DriverResult{Success: false, Error: fmt.Sprintf("delete job: %v", err)}
	}
	return &protocol.DriverResult{Success: true}
}

func statusResult(ctx context.Context, api *NebiusAPI, id string) *protocol.DriverResult {
	job, err := api.GetJob(ctx, id)
	if err != nil {
		return &protocol.DriverResult{Success: false, Error: fmt.Sprintf("get job: %v", err)}
	}

	status := job.GetStatus()
	if status == nil {
		return &protocol.DriverResult{Success: false, Error: "job status not available"}
	}

	state := status.GetState().String()

	var lastRunAt, nextRunAt string
	if status.StartedAt != nil {
		lastRunAt = status.StartedAt.AsTime().Format(time.RFC3339)
	}
	if status.FinishedAt != nil {
		nextRunAt = status.FinishedAt.AsTime().Format(time.RFC3339)
	}

	return &protocol.DriverResult{
		Success: true,
		StatusResult: &protocol.StatusResult{
			ID:        id,
			State:     state,
			LastRunAt: lastRunAt,
			NextRunAt: nextRunAt,
		},
	}
}

func consoleURL(id string) string {
	return "https://console.nebius.com/ai/jobs/" + id
}
