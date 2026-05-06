package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/tomr-ninja/coach"
	"github.com/tomr-ninja/coach/protocol"
)

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx, func() {
		signal.Stop(sigCh)
		cancel()
	}
}

func main() {
	if len(os.Args) < 2 {
		fatal("usage: coach <command> [<args>]\ncommands: run, list-scripts, run-script, schedule")
	}

	switch os.Args[1] {
	case "run":
		var (
			model  string
			data   string
			output string
			force  bool
		)

		runFlags := flag.NewFlagSet("run", flag.ExitOnError)
		runFlags.StringVar(&model, "model", "", "Docker image of the model")
		runFlags.StringVar(&data, "data", "./data", "Path to the data folder")
		runFlags.StringVar(&output, "output", "./output", "Path to the output folder")
		runFlags.BoolVar(&force, "force", false, "Force re-creation of existing artifact")
		if err := runFlags.Parse(os.Args[2:]); err != nil {
			fatal("error parsing flags: %v", err)
		}

		if model == "" || data == "" || output == "" {
			fatal("flags -model, -data, and -output are required")
		}

		if err := coach.ValidateModelImage(model); err != nil {
			fatal("invalid model image: %v", err)
		}
		if err := coach.ValidateDataPath(data); err != nil {
			fatal("invalid data path: %v", err)
		}
		if err := coach.ValidateOutputDir(output); err != nil {
			fatal("invalid output path: %v", err)
		}

		ctx, cancel := signalContext()
		defer cancel()

		fingerprint, err := coach.Run(ctx, model, data, output, force)
		if err != nil {
			fatal("error running coach: %v", err)
		}
		fmt.Printf("%x\n", fingerprint)

	case "list-scripts":
		if len(os.Args) < 3 {
			fatal("usage: coach list-scripts <model-image>")
		}
		modelImage := os.Args[2]
		if err := coach.ValidateModelImage(modelImage); err != nil {
			fatal("invalid model image: %v", err)
		}

		ctx, cancel := signalContext()
		defer cancel()

		scripts, err := coach.ListScripts(ctx, modelImage)
		if err != nil {
			fatal("error listing scripts: %v", err)
		}
		for _, s := range scripts {
			fmt.Println(s)
		}

	case "run-script":
		var (
			data   string
			output string
		)

		runScriptFlags := flag.NewFlagSet("run-script", flag.ExitOnError)
		runScriptFlags.StringVar(&data, "data", "./data", "Path to the data folder")
		runScriptFlags.StringVar(&output, "output", "./output", "Path to the output folder")
		if err := runScriptFlags.Parse(os.Args[2:]); err != nil {
			fatal("error parsing flags: %v", err)
		}

		remaining := runScriptFlags.Args()
		if len(remaining) < 1 {
			fatal("usage: coach run-script [-data <dir>] [-output <dir>] <model-image> <script-name> [args...]")
		}
		modelImage := remaining[0]

		if len(remaining) < 2 {
			fatal("usage: coach run-script [-data <dir>] [-output <dir>] <model-image> <script-name> [args...]")
		}
		scriptName := remaining[1]
		args := remaining[2:]

		if err := coach.ValidateModelImage(modelImage); err != nil {
			fatal("invalid model image: %v", err)
		}
		if err := coach.ValidateScriptName(scriptName); err != nil {
			fatal("invalid script name: %v", err)
		}
		if err := coach.ValidateDataPath(data); err != nil {
			fatal("invalid data path: %v", err)
		}
		if err := coach.ValidateOutputDir(output); err != nil {
			fatal("invalid output path: %v", err)
		}

		ctx, cancel := signalContext()
		defer cancel()

		if err := coach.RunScript(ctx, modelImage, scriptName, args, data, output); err != nil {
			fatal("error running script: %v", err)
		}

	case "schedule":
		if len(os.Args) < 3 {
			fatal("usage: coach schedule <create|list|delete|status> [<args>]")
		}

		switch os.Args[2] {
		case "create":
			var (
				backend    string
				model      string
				outputURI  string
				sched      string
				script     string
				cpu        string
				memory     string
				gpu        string
				gpuType    string
				dataSource string
				command    sliceFlag
				labels     sliceFlag
			)

			createFlags := flag.NewFlagSet("schedule create", flag.ExitOnError)
			createFlags.StringVar(&backend, "backend", "", "Backend name from coach.json")
			createFlags.StringVar(&model, "model", "", "Docker image of the model")
			createFlags.StringVar(&dataSource, "data", "", "Data source (local path or s3://bucket/prefix)")
			createFlags.StringVar(&outputURI, "output", "", "Output destination URI")
			createFlags.StringVar(&sched, "schedule", "", "Cron expression for recurring runs (omit for one-off)")
			createFlags.Var(&command, "command", "Container command override (repeatable)")
			createFlags.StringVar(&script, "script", "", "Script name inside /scripts/")
			createFlags.StringVar(&cpu, "cpu", "", "CPU resources")
			createFlags.StringVar(&memory, "memory", "", "Memory resources")
			createFlags.StringVar(&gpu, "gpu", "", "GPU count")
			createFlags.StringVar(&gpuType, "gpu-type", "", "GPU type")
			createFlags.Var(&labels, "label", "Label key=value (repeatable)")
			if err := createFlags.Parse(os.Args[3:]); err != nil {
				fatal("error parsing flags: %v", err)
			}

			if model == "" || dataSource == "" || outputURI == "" {
				fatal("flags -model, -data, and -output are required")
			}

			if err := coach.ValidateModelImage(model); err != nil {
				fatal("invalid model image: %v", err)
			}
			if err := coach.ValidateDataPath(dataSource); err != nil {
				fatal("invalid data source: %v", err)
			}
			if err := coach.ValidateOutputDir(outputURI); err != nil {
				fatal("invalid output destination: %v", err)
			}
			if err := coach.ValidateCron(sched); err != nil {
				fatal("invalid schedule: %v", err)
			}
			if script != "" {
				if err := coach.ValidateScriptName(script); err != nil {
					fatal("invalid script name: %v", err)
				}
			}

			labelMap := parseLabels(labels)
			resources := protocol.ParseResources(cpu, memory, gpu, gpuType)
			id, err := coach.ScheduleCreate(backend, model, dataSource, outputURI, sched, command, script, resources, labelMap)
			if err != nil {
				fatal("error: %v", err)
			}
			fmt.Println(id)

		case "list":
			var backend string
			listFlags := flag.NewFlagSet("schedule list", flag.ExitOnError)
			listFlags.StringVar(&backend, "backend", "", "Backend name from coach.json")
			if err := listFlags.Parse(os.Args[3:]); err != nil {
				fatal("error parsing flags: %v", err)
			}

			entries, err := coach.ScheduleList(backend)
			if err != nil {
				fatal("error: %v", err)
			}
			for _, e := range entries {
				b, err := json.Marshal(e)
				if err != nil {
					fatal("error marshaling entry: %v", err)
				}
				fmt.Println(string(b))
			}

		case "delete":
			if len(os.Args) < 4 {
				fatal("usage: coach schedule delete <id> [--backend <name>]")
			}
			id := os.Args[3]
			var backend string
			deleteFlags := flag.NewFlagSet("schedule delete", flag.ExitOnError)
			deleteFlags.StringVar(&backend, "backend", "", "Backend name from coach.json")
			if err := deleteFlags.Parse(os.Args[4:]); err != nil {
				fatal("error parsing flags: %v", err)
			}

			if err := coach.ScheduleDelete(backend, id); err != nil {
				fatal("error: %v", err)
			}

		case "status":
			if len(os.Args) < 4 {
				fatal("usage: coach schedule status <id> [--backend <name>]")
			}
			id := os.Args[3]
			var backend string
			statusFlags := flag.NewFlagSet("schedule status", flag.ExitOnError)
			statusFlags.StringVar(&backend, "backend", "", "Backend name from coach.json")
			if err := statusFlags.Parse(os.Args[4:]); err != nil {
				fatal("error parsing flags: %v", err)
			}

			status, err := coach.ScheduleStatus(backend, id)
			if err != nil {
				fatal("error: %v", err)
			}
			if status != nil {
				b, err := json.Marshal(status)
				if err != nil {
					fatal("error marshaling status: %v", err)
				}
				fmt.Println(string(b))
			}

		default:
			fatal("unknown schedule subcommand: %s", os.Args[2])
		}

	default:
		fatal("unknown command: %s", os.Args[1])
	}
}

type sliceFlag []string

func (s *sliceFlag) String() string { return strings.Join(*s, ", ") }

func (s *sliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func parseLabels(labels []string) map[string]string {
	m := make(map[string]string, len(labels))
	for _, l := range labels {
		k, v, found := strings.Cut(l, "=")
		if !found {
			fmt.Fprintf(os.Stderr, "warning: label %q has no '=' separator, using empty value\n", l)
		}
		m[k] = v
	}
	return m
}

func fatal(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
