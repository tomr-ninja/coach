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
	"github.com/tomr-ninja/coach/internal/config"
	coacherrors "github.com/tomr-ninja/coach/internal/errors"
	"github.com/tomr-ninja/coach/internal/validate"
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

	remote := root.Subcommand("remote")
	remote.Subcommand("run")
	remote.Subcommand("schedule")
	remote.Subcommand("ls")
	remote.Subcommand("delete")
	remote.Subcommand("status")

	script := root.Subcommand("script")
	script.Subcommand("ls")
	script.Subcommand("run")

	cmd, err := flag3.ParseCLI(root)
	if err != nil {
		handleError(coacherrors.New(coacherrors.KindUser,
			fmt.Sprintf("invalid command: %v\ncommands: run, script, schedule, cleanup", err)))
	}

	cmd.Next() // go into root

	cfg, err := config.LoadConfig()
	if err != nil {
		handleError(fmt.Errorf("load config: %w", err))
	}

	var verbose bool

	rootFlags := flag.NewFlagSet("coach", flag.ExitOnError)
	rootFlags.BoolVar(&verbose, "verbose", false, "Enable verbose output")
	if err = rootFlags.Parse(cmd.Args()); err != nil {
		handleError(coacherrors.New(coacherrors.KindUser, fmt.Sprintf("error parsing flags: %v", err)))
	}

	if !cmd.Next() {
		handleError(coacherrors.New(coacherrors.KindUser,
			"usage: coach <command> [<args>]\ncommands: run, script, schedule, cleanup"))
	}

	switch cmd.Command() {
	case "run": //nolint:goconst // used in multiple switch cases intentionally
		var data, output string
		var force bool

		runFlags := flag.NewFlagSet("run", flag.ExitOnError)
		runFlags.StringVar(&data, "data", "./data", "Path to the data folder")
		runFlags.StringVar(&output, "output", "./output", "Path to the output folder")
		runFlags.BoolVar(&force, "force", false, "Force re-creation of existing artifact")
		if err = runFlags.Parse(cmd.Args()); err != nil {
			handleError(coacherrors.New(coacherrors.KindUser, fmt.Sprintf("error parsing flags: %v", err)))
		}

		posArgs := runFlags.Args()
		if len(posArgs) < 1 {
			handleError(coacherrors.New(coacherrors.KindUser,
				"usage: coach run [-data <dir>] [-output <dir>] [-force] <model-image>"))
		}
		modelImage := posArgs[0]

		if err = validate.ModelImage(modelImage); err != nil {
			handleError(err)
		}
		err = validate.DirPath(data)
		if err != nil {
			handleError(fmt.Errorf("validate data path: %w", err))
		}
		err = validate.DirPath(output)
		if err != nil {
			handleError(fmt.Errorf("validate output dir: %w", err))
		}

		ctx, cancel := signalContext()
		defer cancel()
		if verbose {
			ctx = coach.WithVerbose(ctx)
		}

		var fingerprint [32]byte
		fingerprint, err = coach.Run(ctx, cfg, modelImage, data, output, force)
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
			if err = validate.ModelImage(modelImage); err != nil {
				handleError(err)
			}

			ctx, cancel := signalContext()
			defer cancel()
			if verbose {
				ctx = coach.WithVerbose(ctx)
			}

			var scripts []string
			scripts, err = coach.ListScripts(ctx, modelImage)
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
			if err = scriptRunFlags.Parse(cmd.Args()); err != nil {
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

			if err = validate.ModelImage(modelImage); err != nil {
				handleError(err)
			}
			if err = validate.ScriptName(scriptName); err != nil {
				handleError(err)
			}
			if err = validate.DirPath(data); err != nil {
				handleError(fmt.Errorf("validate data path: %w", err))
			}
			if err = validate.DirPath(output); err != nil {
				handleError(fmt.Errorf("validate output dir: %w", err))
			}

			ctx, cancel := signalContext()
			defer cancel()
			if verbose {
				ctx = coach.WithVerbose(ctx)
			}

			if err = coach.RunScript(ctx, modelImage, scriptName, scriptArgs, data, output); err != nil {
				handleError(err)
			}

		default:
			handleError(coacherrors.New(coacherrors.KindUser,
				fmt.Sprintf("unknown script subcommand: %s\nusage: coach script <ls|run>", cmd.Command())))
		}

	case "remote":
		var backend string

		remoteFlags := flag.NewFlagSet("remote", flag.ExitOnError)
		remoteFlags.StringVar(&backend, "backend", "", "Backend name from coach.json")
		if err = remoteFlags.Parse(cmd.Args()); err != nil {
			handleError(coacherrors.New(coacherrors.KindUser, fmt.Sprintf("error parsing flags: %v", err)))
		}

		if !cmd.Next() {
			handleError(coacherrors.New(coacherrors.KindUser,
				"usage: coach remote [-backend <name>] <run|schedule|ls|delete|status> [<args>]"))
		}

		switch cmd.Command() {
		case "run":
			var (
				dataSource, outputURI, script string
				force, watch                  bool
				cpu, memory, gpu, gpuType     string
				command, labels               sliceFlag
			)

			runFlags := flag.NewFlagSet("remote run", flag.ExitOnError)
			runFlags.StringVar(&dataSource, "data", "", "Data source (local path or s3://bucket/prefix)")
			runFlags.StringVar(&outputURI, "output", "", "Output destination URI")
			runFlags.BoolVar(&force, "force", false, "Override existing artifact")
			runFlags.BoolVar(&watch, "watch", false, "Watch remote logs after submission")
			runFlags.Var(&command, "command", "Container command override (repeatable)")
			runFlags.StringVar(&script, "script", "", "Script name inside /scripts/")
			runFlags.StringVar(&cpu, "cpu", "", "CPU resources")
			runFlags.StringVar(&memory, "memory", "", "Memory resources")
			runFlags.StringVar(&gpu, "gpu", "", "GPU count")
			runFlags.StringVar(&gpuType, "gpu-type", "", "GPU type")
			runFlags.Var(&labels, "label", "Label key=value (repeatable)")
			if err = runFlags.Parse(cmd.Args()); err != nil {
				handleError(coacherrors.New(coacherrors.KindUser, fmt.Sprintf("error parsing flags: %v", err)))
			}

			posArgs := runFlags.Args()
			if len(posArgs) < 1 {
				handleError(coacherrors.New(coacherrors.KindUser,
					"usage: coach remote [-backend <name>] run [flags] <model-image>"))
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
			if script != "" {
				if err := validate.ScriptName(script); err != nil {
					handleError(err)
				}
			}

			labelMap := parseLabels(labels)
			resources := validate.ParseResources(cpu, memory, gpu, gpuType)
			ctx, cancel := signalContext()
			defer cancel()
			if verbose {
				ctx = coach.WithVerbose(ctx)
			}
			id, logS3URI, err := coach.RemoteRun(ctx, cfg, backend, modelImage, dataSource, outputURI, command, script, resources, labelMap, force)
			if err != nil {
				handleError(err)
			}
			fmt.Println(id)

			if watch {
				coach.WatchS3Log(ctx, cfg, logS3URI)
			}

		case "schedule":
			var (
				dataSource, outputURI, sched, script string
				cpu, memory, gpu, gpuType            string
				command, labels                      sliceFlag
			)

			schedFlags := flag.NewFlagSet("remote schedule", flag.ExitOnError)
			schedFlags.StringVar(&dataSource, "data", "", "Data source (local path or s3://bucket/prefix)")
			schedFlags.StringVar(&outputURI, "output", "", "Output destination URI")
			schedFlags.StringVar(&sched, "schedule", "", "Cron expression for recurring runs (required)")
			schedFlags.Var(&command, "command", "Container command override (repeatable)")
			schedFlags.StringVar(&script, "script", "", "Script name inside /scripts/")
			schedFlags.StringVar(&cpu, "cpu", "", "CPU resources")
			schedFlags.StringVar(&memory, "memory", "", "Memory resources")
			schedFlags.StringVar(&gpu, "gpu", "", "GPU count")
			schedFlags.StringVar(&gpuType, "gpu-type", "", "GPU type")
			schedFlags.Var(&labels, "label", "Label key=value (repeatable)")
			if err := schedFlags.Parse(cmd.Args()); err != nil {
				handleError(coacherrors.New(coacherrors.KindUser, fmt.Sprintf("error parsing flags: %v", err)))
			}

			posArgs := schedFlags.Args()
			if len(posArgs) < 1 {
				handleError(coacherrors.New(coacherrors.KindUser,
					"usage: coach remote [-backend <name>] schedule [flags] <model-image>"))
			}
			modelImage := posArgs[0]

			if dataSource == "" || outputURI == "" {
				handleError(coacherrors.New(coacherrors.KindUser, "flags -data and -output are required"))
			}
			if sched == "" {
				handleError(coacherrors.New(coacherrors.KindUser, "-schedule is required for recurring runs (use 'remote run' for one-off)"))
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
			resources := validate.ParseResources(cpu, memory, gpu, gpuType)
			ctx, cancel := signalContext()
			defer cancel()
			if verbose {
				ctx = coach.WithVerbose(ctx)
			}
			id, err := coach.RemoteSchedule(ctx, cfg, backend, modelImage, dataSource, outputURI, sched, command, script, resources, labelMap)
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
			entries, err := coach.RemoteList(ctx, cfg, backend)
			if err != nil {
				handleError(err)
			}
			for _, e := range entries {
				b, err := json.Marshal(e)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: marshal entry: %v\n", err)
					continue
				}
				fmt.Println(string(b))
			}

		case "delete":
			if len(cmd.Args()) < 1 {
				handleError(coacherrors.New(coacherrors.KindUser,
					"usage: coach remote [-backend <name>] delete <id>"))
			}
			id := cmd.Args()[0]
			ctx, cancel := signalContext()
			defer cancel()
			if verbose {
				ctx = coach.WithVerbose(ctx)
			}
			if err := coach.RemoteDelete(ctx, cfg, backend, id); err != nil {
				handleError(err)
			}

		case "status":
			if len(cmd.Args()) < 1 {
				handleError(coacherrors.New(coacherrors.KindUser,
					"usage: coach remote [-backend <name>] status <id>"))
			}
			id := cmd.Args()[0]
			ctx, cancel := signalContext()
			defer cancel()
			if verbose {
				ctx = coach.WithVerbose(ctx)
			}
			status, err := coach.RemoteStatus(ctx, cfg, backend, id)
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
				fmt.Sprintf("unknown remote subcommand: %s\nusage: coach remote <run|schedule|ls|delete|status>", cmd.Command())))
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
			fmt.Sprintf("unknown command: %s\ncommands: run, script, remote, cleanup", cmd.Command())))
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
