package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/tomr-ninja/coach/protocol"
)

type driverOutput struct {
	Success        bool              `json:"success"`
	ScheduledRunID string            `json:"scheduledRunId,omitempty"`
	URL            string            `json:"url,omitempty"`
	Entries        []json.RawMessage `json:"entries,omitempty"`
	Status         json.RawMessage   `json:"status,omitempty"`
	Error          string            `json:"error,omitempty"`
}

func main() {
	cfgRaw := os.Getenv("COACH_BACKEND_CONFIG")
	var cfg struct {
		Region  string `json:"region"`
		Project string `json:"project_id"`
		Token   string `json:"secret_key"`
		Org     string `json:"organization_id"`
	}
	if cfgRaw != "" {
		if err := json.Unmarshal([]byte(cfgRaw), &cfg); err != nil {
			writeError("parse COACH_BACKEND_CONFIG: " + err.Error())
			os.Exit(1)
		}
	}

	if cfg.Token == "" {
		writeError("secret_key is required. Set it in coach.json backends.scaleway.config")
		os.Exit(1)
	}
	if cfg.Project == "" {
		writeError("project_id is required. Set it in coach.json backends.scaleway.config")
		os.Exit(1)
	}
	if cfg.Region == "" {
		cfg.Region = "fr-par"
	}

	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		writeError("read stdin: " + err.Error())
		os.Exit(1)
	}

	var spec protocol.JobSpec
	if uerr := json.Unmarshal(input, &spec); uerr != nil {
		writeError("parse job spec: " + uerr.Error())
		os.Exit(1)
	}

	baseURL := "https://api.scaleway.com/serverless-jobs/v1alpha2/regions/" + cfg.Region
	api := NewScalewayAPI(baseURL, cfg.Token, cfg.Region, cfg.Project, cfg.Org)

	var result driverOutput
	var raw map[string]any
	var opErr error

	switch spec.Operation {
	case "run":
		raw, opErr = run(api, &spec)
	case "schedule":
		raw, opErr = schedule(api, &spec)
	case "list":
		raw, opErr = list(api)
	case "delete":
		raw, opErr = deleteOp(api, spec.ScheduledRunID)
	case "status":
		raw, opErr = statusOp(api, spec.ScheduledRunID)
	default:
		result = driverOutput{Success: false, Error: fmt.Sprintf("unknown operation: %s", spec.Operation)}
	}

	if opErr != nil {
		fmt.Fprintf(os.Stderr, "%v\n", opErr)
		result = driverOutput{Success: false, Error: opErr.Error()}
	}

	if raw != nil {
		marshalResult(raw, &result)
	}

	out, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal result: %v\n", err)
		out = []byte(`{"success":false,"error":"marshal result"}`)
	}
	fmt.Println(string(out))
	os.Exit(0)
}

func marshalResult(raw map[string]any, result *driverOutput) {
	if v, ok := raw["success"].(bool); ok {
		result.Success = v
	}
	if v, ok := raw["scheduledRunId"].(string); ok {
		result.ScheduledRunID = v
	}
	if v, ok := raw["url"].(string); ok {
		result.URL = v
	}
	if v, ok := raw["error"].(string); ok {
		result.Error = v
	}
	if entries, ok := raw["entries"].([]map[string]any); ok {
		for _, e := range entries {
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			result.Entries = append(result.Entries, b)
		}
	}
	if status, ok := raw["status"].(map[string]any); ok {
		b, err := json.Marshal(status)
		if err == nil {
			result.Status = b
		}
	}
}

func writeError(msg string) {
	fmt.Printf(`{"success":false,"error":%s}`, jsonMarshal(msg))
}

func jsonMarshal(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}
