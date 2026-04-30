package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tomr-ninja/coach"
)

func main() {
	if len(os.Args) < 2 {
		fatal("usage: coach <command> [<args>]\ncommands: run, list-scripts, run-script")
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

		fingerprint, err := coach.Run(model, data, output, force)
		if err != nil {
			fatal("error running coach: %v", err)
		}
		fmt.Printf("%x\n", fingerprint)

	case "list-scripts":
		if len(os.Args) < 3 {
			fatal("usage: coach list-scripts <model-image>")
		}
		modelImage := os.Args[2]

		scripts, err := coach.ListScripts(modelImage)
		if err != nil {
			fatal("error listing scripts: %v", err)
		}
		for _, s := range scripts {
			fmt.Println(s)
		}

	case "run-script":
		if len(os.Args) < 3 {
			fatal("usage: coach run-script <model-image> <script-name> [args...]")
		}
		modelImage := os.Args[2]

		if len(os.Args) < 4 {
			fatal("usage: coach run-script <model-image> <script-name> [args...]")
		}
		scriptName := os.Args[3]
		args := os.Args[4:]

		if err := coach.RunScript(modelImage, scriptName, args); err != nil {
			fatal("error running script: %v", err)
		}

	default:
		fatal("unknown command: %s", os.Args[1])
	}
}

func fatal(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
