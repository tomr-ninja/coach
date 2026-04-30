package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tomr-ninja/coach"
)

func main() {
	if len(os.Args) < 2 {
		fatal("usage: coach <command> [<args>]\ncommands: run")
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

	default:
		fatal("unknown command: %s", os.Args[1])
	}
}

func fatal(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
