// Command aws-account-provisioner provisions AWS accounts through Control Tower
// Account Factory and assigns IAM Identity Center permission sets.
//
// See docs/SPEC.md for the full specification.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// version is overridden at build time via -ldflags.
var version = "dev"

// errNotImplemented is returned by the command stubs until each feature lands.
var errNotImplemented = errors.New("not implemented yet")

type options struct {
	init        bool
	dryRun      bool
	profile     string
	showVersion bool
	inputPath   string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}

	switch {
	case opts.showVersion:
		fmt.Printf("aws-account-provisioner %s\n", version)
		return nil
	case opts.init:
		return runInit(opts)
	case opts.dryRun:
		return runDryRun(opts)
	default:
		return runProvision(opts)
	}
}

func parseArgs(args []string) (options, error) {
	var opts options

	fs := flag.NewFlagSet("aws-account-provisioner", flag.ContinueOnError)
	fs.BoolVar(&opts.init, "init", false, "launch the interactive wizard and write input.tsv")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "validate input and AWS state without creating anything")
	fs.StringVar(&opts.profile, "profile", "", "AWS profile used to reach the management account")
	fs.BoolVar(&opts.showVersion, "version", false, "print the version and exit")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return opts, err
	}

	if opts.init && opts.dryRun {
		return opts, errors.New("--init and --dry-run cannot be combined")
	}

	// Every mode except --init and --version needs an input file.
	if !opts.init && !opts.showVersion {
		if fs.NArg() != 1 {
			fs.Usage()
			return opts, errors.New("exactly one input file must be given")
		}
		opts.inputPath = fs.Arg(0)
	}

	return opts, nil
}

const usage = `aws-account-provisioner - provision AWS accounts via Control Tower Account Factory

Usage:
  aws-account-provisioner --init
  aws-account-provisioner --dry-run <input.tsv>
  aws-account-provisioner <input.tsv>

Flags:
`

func runInit(_ options) error      { return errNotImplemented }
func runDryRun(_ options) error    { return errNotImplemented }
func runProvision(_ options) error { return errNotImplemented }
