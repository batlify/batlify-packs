package cmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/batlify/batlify-packs/internal/build"
	"github.com/batlify/batlify-packs/internal/plan"
)

const version = "0.1.0"

// Execute runs the CLI with the current process arguments and standard streams.
func Execute() error { return ExecuteArgs(os.Args[1:], os.Stdout, os.Stderr) }

// ExecuteArgs is kept separate from process state so the CLI can be verified
// without starting a Docker daemon.
func ExecuteArgs(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return printUsage(stdout)
	}
	if args[0] == "--version" || args[0] == "version" {
		_, err := fmt.Fprintln(stdout, version)
		return err
	}

	switch args[0] {
	case "plan":
		return runPlan(args[1:], stdout, stderr)
	case "build":
		return runBuild(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage())
	}
}

func runPlan(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(moveFlagsFirst(args, nil)); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return errors.New("plan accepts at most one project path")
	}
	root := "."
	if flags.NArg() == 1 {
		root = flags.Arg(0)
	}
	p, err := plan.Detect(root)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

func runBuild(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	image := flags.String("name", "", "name and optional tag for the image")
	flags.StringVar(image, "t", "", "name and optional tag for the image (shorthand)")
	noCache := flags.Bool("no-cache", false, "build without reusing Docker build layers")
	if err := flags.Parse(moveFlagsFirst(args, map[string]bool{"--name": true, "-t": true})); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return errors.New("build accepts at most one project path")
	}
	root := "."
	if flags.NArg() == 1 {
		root = flags.Arg(0)
	}
	p, err := plan.Detect(root)
	if err != nil {
		return err
	}
	if *image == "" {
		*image = defaultImageName(filepath.Base(p.ProjectDir))
	}
	if err := build.BuildWithOptions(p, *image, stdout, stderr, build.Options{NoCache: *noCache}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Built image %s\n", *image)
	return err
}

func usage() string {
	return `Usage:
  batlify-packs plan [PATH]
  batlify-packs build [PATH] [--name IMAGE]

Commands:
  plan    Detect a supported project and print its build plan as JSON
  build   Build a Docker image from a supported project

Examples:
  batlify-packs plan .
  batlify-packs build . --name my-app:latest`
}

func printUsage(w io.Writer) error {
	_, err := fmt.Fprintln(w, usage())
	return err
}

func moveFlagsFirst(args []string, valueFlags map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			flags = append(flags, arg)
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}
		flags = append(flags, arg)
		if valueFlags[arg] && !strings.Contains(arg, "=") && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

func defaultImageName(dir string) string {
	dir = strings.ToLower(dir)
	var b strings.Builder
	for _, r := range dir {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-._")
	if name == "" {
		name = "app"
	}
	return name + ":latest"
}
