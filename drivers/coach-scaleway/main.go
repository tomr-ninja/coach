package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/tomr-ninja/coach/protocol"
)

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
			writeResult(&protocol.DriverResult{Success: false, Error: "parse COACH_BACKEND_CONFIG: " + err.Error()})
			os.Exit(0)
		}
	}

	if cfg.Token == "" {
		writeResult(&protocol.DriverResult{Success: false, Error: "secret_key is required. Set it in coach.json backends.scaleway.config"})
		os.Exit(0)
	}
	if cfg.Project == "" {
		writeResult(&protocol.DriverResult{Success: false, Error: "project_id is required. Set it in coach.json backends.scaleway.config"})
		os.Exit(0)
	}
	if cfg.Region == "" {
		cfg.Region = "fr-par"
	}

	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		writeResult(&protocol.DriverResult{Success: false, Error: "read stdin: " + err.Error()})
		os.Exit(0)
	}

	var spec protocol.Spec
	if uerr := json.Unmarshal(input, &spec); uerr != nil {
		writeResult(&protocol.DriverResult{Success: false, Error: "parse spec: " + uerr.Error()})
		os.Exit(0)
	}

	baseURL := "https://api.scaleway.com/serverless-jobs/v1alpha2/regions/" + cfg.Region
	api := NewScalewayAPI(baseURL, cfg.Token, cfg.Region, cfg.Project, cfg.Org)

	var result *protocol.DriverResult

	switch spec.Type {
	case "submit":
		result = submit(api, spec.Job)
	case "list":
		result = listResult(api)
	case "delete":
		result = deleteResult(api, spec.ID)
	case "status":
		result = statusResult(api, spec.ID)
	default:
		result = &protocol.DriverResult{Success: false, Error: fmt.Sprintf("unknown type: %s", spec.Type)}
	}

	writeResult(result)
	os.Exit(0)
}

func writeResult(result *protocol.DriverResult) {
	result.ProtocolVersion = protocol.ProtocolVersion
	out, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal result: %v\n", err)
		bailout := `{"success":false,"error":"marshal result","protocolVersion":1}`
		fmt.Println(bailout)
		return
	}
	fmt.Println(string(out))
}
