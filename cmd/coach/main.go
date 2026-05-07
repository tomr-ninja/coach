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

	"github.com/tomr-ninja/flag3"

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
	tree := flag3.NewCLI()
	tree.Subcommand("run")
	script := tree.Subcommand("script")
	script.Subcommand("ls")
	script.Subcommand("run")
	schedule := tree.Subcommand("schedule")
	schedule.Subcommand("create")
	schedule.Subcommand("ls")
	schedule.Subcommand("delete")
	schedule.Subcommand("status")

	cmd, err := flag3.ParseCLI(tree)
	if err != nil {
		fatal("invalid command: %v\ncommands: run, script, schedule", err)
	}

	cmd.Next() // skip root (coach)

	if !cmd.Next() {
		fatal("usage: coach <command> [<args>]\ncommands: run, script, schedule")
	}

	switch cmd.Command() {
	case "run":
		var data, output string
		var force bool

		runFlags := flag.NewFlagSet("run", flag.ExitOnError)
		runFlags.StringVar(&data, "data", "./data", "Path to the data folder")
		runFlags.StringVar(&output, "output", "./output", "Path to the output folder")
		runFlags.BoolVar(&force, "force", false, "Force re-creation of existing artifact")
		if err := runFlags.Parse(cmd.Args()); err != nil {
			fatal("error parsing flags: %v", err)
		}

		posArgs := runFlags.Args()
		if len(posArgs) < 1 {
			fatal("usage: coach run [-data <dir>] [-output <dir>] [-force] <model-image>")
		}
		modelImage := posArgs[0]

		if err := coach.ValidateModelImage(modelImage); err != nil {
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

		fingerprint, err := coach.Run(ctx, modelImage, data, output, force)
		if err != nil {
			fatal("error running coach: %v", err)
		}
		fmt.Printf("%x\n", fingerprint)

	case "script":
		if !cmd.Next() {
			fatal("usage: coach script <ls|run> [<args>]")
		}

		switch cmd.Command() {
		case "ls":
			if len(cmd.Args()) < 1 {
				fatal("usage: coach script ls <model-image>")
			}
			modelImage := cmd.Args()[0]
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

		case "run":
			var data, output string

			scriptRunFlags := flag.NewFlagSet("script run", flag.ExitOnError)
			scriptRunFlags.StringVar(&data, "data", "./data", "Path to the data folder")
			scriptRunFlags.StringVar(&output, "output", "./output", "Path to the output folder")
			if err := scriptRunFlags.Parse(cmd.Args()); err != nil {
				fatal("error parsing flags: %v", err)
			}

			remaining := scriptRunFlags.Args()
			if len(remaining) < 1 {
				fatal("usage: coach script run [-data <dir>] [-output <dir>] <model-image> <script-name> [args...]")
			}
			modelImage := remaining[0]

			if len(remaining) < 2 {
				fatal("usage: coach script run [-data <dir>] [-output <dir>] <model-image> <script-name> [args...]")
			}
			scriptName := remaining[1]
			scriptArgs := remaining[2:]

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

			if err := coach.RunScript(ctx, modelImage, scriptName, scriptArgs, data, output); err != nil {
				fatal("error running script: %v", err)
			}

		default:
			fatal("unknown script subcommand: %s\nusage: coach script <ls|run>", cmd.Command())
		}

	case "schedule":
		var backend string

		schedFlags := flag.NewFlagSet("schedule", flag.ExitOnError)
		schedFlags.StringVar(&backend, "backend", "", "Backend name from coach.json")
		if err := schedFlags.Parse(cmd.Args()); err != nil {
			fatal("error parsing flags: %v", err)
		}

		if !cmd.Next() {
			fatal("usage: coach schedule [-backend <name>] <create|ls|delete|status> [<args>]")
		}

		switch cmd.Command() {
		case "create":
			var (
				dataSource, outputURI, sched, script string
				cpu, memory, gpu, gpuType            string
				command, labels                      sliceFlag
			)

			createFlags := flag.NewFlagSet("schedule create", flag.ExitOnError)
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
			if err := createFlags.Parse(cmd.Args()); err != nil {
				fatal("error parsing flags: %v", err)
			}

			posArgs := createFlags.Args()
			if len(posArgs) < 1 {
				fatal("usage: coach schedule [-backend <name>] create [flags] <model-image>")
			}
			modelImage := posArgs[0]

			if dataSource == "" || outputURI == "" {
				fatal("flags -data and -output are required")
			}

			if err := coach.ValidateModelImage(modelImage); err != nil {
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
			id, err := coach.ScheduleCreate(backend, modelImage, dataSource, outputURI, sched, command, script, resources, labelMap)
			if err != nil {
				fatal("error: %v", err)
			}
			fmt.Println(id)

		case "ls":
			entries, err := coach.ScheduleList(backend)
			if err != nil {
				fatal("error: %v", err)
			}
			for _, e := range entries {
				b, _ := json.Marshal(e)
				fmt.Println(string(b))
			}

		case "delete":
			if len(cmd.Args()) < 1 {
				fatal("usage: coach schedule [-backend <name>] delete <id>")
			}
			id := cmd.Args()[0]
			if err := coach.ScheduleDelete(backend, id); err != nil {
				fatal("error: %v", err)
			}

		case "status":
			if len(cmd.Args()) < 1 {
				fatal("usage: coach schedule [-backend <name>] status <id>")
			}
			id := cmd.Args()[0]
			status, err := coach.ScheduleStatus(backend, id)
			if err != nil {
				fatal("error: %v", err)
			}
			if status != nil {
				b, _ := json.Marshal(status)
				fmt.Println(string(b))
			}

		default:
			fatal("unknown schedule subcommand: %s\nusage: coach schedule <create|ls|delete|status>", cmd.Command())
		}

	default:
		fatal("unknown command: %s\ncommands: run, script, schedule", cmd.Command())
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
