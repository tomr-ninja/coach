package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/tomr-ninja/coach/protocol"
)

func main() {
	cfgRaw := os.Getenv("COACH_BACKEND_CONFIG")
	var cfg nebiusConfig
	if cfgRaw != "" {
		if err := json.Unmarshal([]byte(cfgRaw), &cfg); err != nil {
			writeResult(&protocol.DriverResult{Success: false, Error: "parse COACH_BACKEND_CONFIG: " + err.Error()})
			os.Exit(0)
		}
	}

	if cfg.ProjectID == "" {
		writeResult(&protocol.DriverResult{Success: false, Error: "project_id is required. Set it in coach.json backends.nebius.config"})
		os.Exit(0)
	}
	if cfg.SubnetID == "" {
		writeResult(&protocol.DriverResult{Success: false, Error: "subnet_id is required. Set it in coach.json backends.nebius.config"})
		os.Exit(0)
	}

	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		writeResult(&protocol.DriverResult{Success: false, Error: "read stdin: " + err.Error()})
		os.Exit(0)
	}

	var spec protocol.Spec
	if err := json.Unmarshal(input, &spec); err != nil {
		writeResult(&protocol.DriverResult{Success: false, Error: "parse spec: " + err.Error()})
		os.Exit(0)
	}

	ctx := context.Background()
	api, err := NewNebiusAPI(ctx, &cfg)
	if err != nil {
		writeResult(&protocol.DriverResult{Success: false, Error: "init nebius api: " + err.Error()})
		os.Exit(0)
	}
	defer api.Close()

	var result *protocol.DriverResult

	switch spec.Type {
	case "submit":
		result = submit(ctx, api, spec.Job)
	case "list":
		result = listResult(ctx, api)
	case "delete":
		result = deleteResult(ctx, api, spec.ID)
	case "status":
		result = statusResult(ctx, api, spec.ID)
	default:
		result = &protocol.DriverResult{Success: false, Error: fmt.Sprintf("unknown type: %s", spec.Type)}
	}

	writeResult(result)
	os.Exit(0)
}

func writeResult(result *protocol.DriverResult) {
	result.ProtocolVersion = protocol.Version
	out, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal result: %v\n", err)
		bailout := `{"success":false,"error":"marshal result","protocolVersion":1}`
		fmt.Println(bailout)
		return
	}
	fmt.Println(string(out))
}
