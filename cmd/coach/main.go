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
	"time"

	"github.com/tomr-ninja/flag3"

	"github.com/tomr-ninja/coach"
	coacherrors "github.com/tomr-ninja/coach/internal/errors"
	"github.com/tomr-ninja/coach/internal/validate"
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
	root := flag3.NewCLI()
	root.Subcommand("cleanup")
	root.Subcommand("run")

	schedule := root.Subcommand("schedule")
	schedule.Subcommand("create")
	schedule.Subcommand("ls")
	schedule.Subcommand("delete")
	schedule.Subcommand("status")

	script := root.Subcommand("script")
	script.Subcommand("ls")
	script.Subcommand("run")

	cmd, err := flag3.ParseCLI(root)
	if err != nil {
		handleError(coacherrors.New(coacherrors.KindUser,
			fmt.Sprintf("invalid command: %v\ncommands: run, script, schedule, cleanup", err)))
	}

	cmd.Next() // go into root

	var verbose bool

	rootFlags := flag.NewFlagSet("coach", flag.ExitOnError)
	rootFlags.BoolVar(&verbose, "verbose", false, "Enable verbose output")
	if err := rootFlags.Parse(cmd.Args()); err != nil {
		handleError(coacherrors.New(coacherrors.KindUser, fmt.Sprintf("error parsing flags: %v", err)))
	}

	if !cmd.Next() {
		handleError(coacherrors.New(coacherrors.KindUser,
			"usage: coach <command> [<args>]\ncommands: run, script, schedule, cleanup"))
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
			handleError(coacherrors.New(coacherrors.KindUser, fmt.Sprintf("error parsing flags: %v", err)))
		}

		posArgs := runFlags.Args()
		if len(posArgs) < 1 {
			handleError(coacherrors.New(coacherrors.KindUser,
				"usage: coach run [-data <dir>] [-output <dir>] [-force] <model-image>"))
		}
		modelImage := posArgs[0]

		if err := validate.ModelImage(modelImage); err != nil {
			handleError(err)
		}
		if err := validate.DirPath(data); err != nil {
			handleError(fmt.Errorf("validate data path: %w", err))
		}
		if err := validate.DirPath(output); err != nil {
			handleError(fmt.Errorf("validate output dir: %w", err))
		}

		ctx, cancel := signalContext()
		defer cancel()
		if verbose {
			ctx = coach.WithVerbose(ctx)
		}

		fingerprint, err := coach.Run(ctx, modelImage, data, output, force)
		if err != nil {
			handleError(err)
		}
		fmt.Printf("%x\n", fingerprint)

	case "script":
		if !cmd.Next() {
			handleError(coacherrors.New(coacherrors.KindUser,
				"usage: coach script <ls|run> [<args>]"))
		}

		switch cmd.Command() {
		case "ls":
			if len(cmd.Args()) < 1 {
				handleError(coacherrors.New(coacherrors.KindUser,
					"usage: coach script ls <model-image>"))
			}
			modelImage := cmd.Args()[0]
			if err := validate.ModelImage(modelImage); err != nil {
				handleError(err)
			}

			ctx, cancel := signalContext()
			defer cancel()
			if verbose {
				ctx = coach.WithVerbose(ctx)
			}

			scripts, err := coach.ListScripts(ctx, modelImage)
			if err != nil {
				handleError(err)
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
				handleError(coacherrors.New(coacherrors.KindUser, fmt.Sprintf("error parsing flags: %v", err)))
			}

			remaining := scriptRunFlags.Args()
			if len(remaining) < 1 {
				handleError(coacherrors.New(coacherrors.KindUser,
					"usage: coach script run [-data <dir>] [-output <dir>] <model-image> <script-name> [args...]"))
			}
			modelImage := remaining[0]

			if len(remaining) < 2 {
				handleError(coacherrors.New(coacherrors.KindUser,
					"usage: coach script run [-data <dir>] [-output <dir>] <model-image> <script-name> [args...]"))
			}
			scriptName := remaining[1]
			scriptArgs := remaining[2:]

			if err := validate.ModelImage(modelImage); err != nil {
				handleError(err)
			}
			if err := validate.ScriptName(scriptName); err != nil {
				handleError(err)
			}
			if err := validate.DirPath(data); err != nil {
				handleError(fmt.Errorf("validate data path: %w", err))
			}
			if err := validate.DirPath(output); err != nil {
				handleError(fmt.Errorf("validate output dir: %w", err))
			}

			ctx, cancel := signalContext()
			defer cancel()
			if verbose {
				ctx = coach.WithVerbose(ctx)
			}

			if err := coach.RunScript(ctx, modelImage, scriptName, scriptArgs, data, output); err != nil {
				handleError(err)
			}

		default:
			handleError(coacherrors.New(coacherrors.KindUser,
				fmt.Sprintf("unknown script subcommand: %s\nusage: coach script <ls|run>", cmd.Command())))
		}

	case "schedule":
		var backend string

		schedFlags := flag.NewFlagSet("schedule", flag.ExitOnError)
		schedFlags.StringVar(&backend, "backend", "", "Backend name from coach.json")
		if err := schedFlags.Parse(cmd.Args()); err != nil {
			handleError(coacherrors.New(coacherrors.KindUser, fmt.Sprintf("error parsing flags: %v", err)))
		}

		if !cmd.Next() {
			handleError(coacherrors.New(coacherrors.KindUser,
				"usage: coach schedule [-backend <name>] <create|ls|delete|status> [<args>]"))
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
				handleError(coacherrors.New(coacherrors.KindUser, fmt.Sprintf("error parsing flags: %v", err)))
			}

			posArgs := createFlags.Args()
			if len(posArgs) < 1 {
				handleError(coacherrors.New(coacherrors.KindUser,
					"usage: coach schedule [-backend <name>] create [flags] <model-image>"))
			}
			modelImage := posArgs[0]

			if dataSource == "" || outputURI == "" {
				handleError(coacherrors.New(coacherrors.KindUser, "flags -data and -output are required"))
			}

			if err := validate.ModelImage(modelImage); err != nil {
				handleError(err)
			}
			if err := validate.DirPath(dataSource); err != nil {
				handleError(fmt.Errorf("validate data path: %w", err))
			}
			if err := validate.DirPath(outputURI); err != nil {
				handleError(fmt.Errorf("validate output dir: %w", err))
			}
			if err := validate.Cron(sched); err != nil {
				handleError(err)
			}
			if script != "" {
				if err := validate.ScriptName(script); err != nil {
					handleError(err)
				}
			}

			labelMap := parseLabels(labels)
			resources := protocol.ParseResources(cpu, memory, gpu, gpuType)
			ctx, cancel := signalContext()
			defer cancel()
			if verbose {
				ctx = coach.WithVerbose(ctx)
			}
			id, err := coach.ScheduleCreate(ctx, backend, modelImage, dataSource, outputURI, sched, command, script, resources, labelMap)
			if err != nil {
				handleError(err)
			}
			fmt.Println(id)

		case "ls":
			ctx, cancel := signalContext()
			defer cancel()
			if verbose {
				ctx = coach.WithVerbose(ctx)
			}
			entries, err := coach.ScheduleList(ctx, backend)
			if err != nil {
				handleError(err)
			}
			for _, e := range entries {
				b, err := json.Marshal(e)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: marshal schedule entry: %v\n", err)
					continue
				}
				fmt.Println(string(b))
			}

		case "delete":
			if len(cmd.Args()) < 1 {
				handleError(coacherrors.New(coacherrors.KindUser,
					"usage: coach schedule [-backend <name>] delete <id>"))
			}
			id := cmd.Args()[0]
			ctx, cancel := signalContext()
			defer cancel()
			if verbose {
				ctx = coach.WithVerbose(ctx)
			}
			if err := coach.ScheduleDelete(ctx, backend, id); err != nil {
				handleError(err)
			}

		case "status":
			if len(cmd.Args()) < 1 {
				handleError(coacherrors.New(coacherrors.KindUser,
					"usage: coach schedule [-backend <name>] status <id>"))
			}
			id := cmd.Args()[0]
			ctx, cancel := signalContext()
			defer cancel()
			if verbose {
				ctx = coach.WithVerbose(ctx)
			}
			status, err := coach.ScheduleStatus(ctx, backend, id)
			if err != nil {
				handleError(err)
			}
			if status != nil {
				b, err := json.Marshal(status)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: marshal status: %v\n", err)
				} else {
					fmt.Println(string(b))
				}
			}

		default:
			handleError(coacherrors.New(coacherrors.KindUser,
				fmt.Sprintf("unknown schedule subcommand: %s\nusage: coach schedule <create|ls|delete|status>", cmd.Command())))
		}

	case "cleanup":
		var (
			dryRun bool
			older  string
		)

		cleanupFlags := flag.NewFlagSet("cleanup", flag.ExitOnError)
		cleanupFlags.BoolVar(&dryRun, "dry-run", false, "Print images that would be removed without removing them")
		cleanupFlags.StringVar(&older, "older", "24h", "Remove images older than duration (e.g. 24h, 7d)")
		if err := cleanupFlags.Parse(cmd.Args()); err != nil {
			handleError(coacherrors.New(coacherrors.KindUser, fmt.Sprintf("error parsing flags: %v", err)))
		}

		var maxAge time.Duration
		if older != "" {
			var parseErr error
			maxAge, parseErr = time.ParseDuration(older)
			if parseErr != nil {
				handleError(coacherrors.New(coacherrors.KindUser,
					fmt.Sprintf("invalid duration %q (examples: 24h, 7d, 30m): %v", older, parseErr)))
			}
		}

		ctx, cancel := signalContext()
		defer cancel()
		if verbose {
			ctx = coach.WithVerbose(ctx)
		}

		result, err := coach.Cleanup(ctx, maxAge, dryRun)
		if err != nil {
			handleError(err)
		}
		fmt.Printf("Cleanup complete: %d images removed, %d errors\n", result.Removed, result.Errors)

	default:
		handleError(coacherrors.New(coacherrors.KindUser,
			fmt.Sprintf("unknown command: %s\ncommands: run, script, schedule, cleanup", cmd.Command())))
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

func handleError(err error) {
	kind := coacherrors.GetKind(err)
	hint := coacherrors.GetHint(err)

	fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
	if hint != "" {
		fmt.Fprintf(os.Stderr, "Hint: %s\n", hint)
	}

	os.Exit(coacherrors.ExitCode(kind))
}
