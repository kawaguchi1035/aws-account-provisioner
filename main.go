// Command aws-account-provisioner provisions AWS accounts through Control Tower
// Account Factory and assigns IAM Identity Center permission sets.
//
// See docs/SPEC.md for the full specification.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kawaguchi1035/aws-account-provisioner/internal/awsauth"
	"github.com/kawaguchi1035/aws-account-provisioner/internal/config"
	"github.com/kawaguchi1035/aws-account-provisioner/internal/discovery"
	"github.com/kawaguchi1035/aws-account-provisioner/internal/inputfile"
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
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}

	switch {
	case opts.showVersion:
		printer{out}.printf("aws-account-provisioner %s\n", version)
		return nil
	case opts.init:
		return runInit(opts)
	case opts.dryRun:
		return runDryRun(ctx, opts, out)
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
		_, _ = fmt.Fprint(fs.Output(), usage)
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

// plan is the validated result of everything that must be true before any
// account is created.
type plan struct {
	accounts []inputfile.Account
	catalog  *discovery.Catalog
}

// preflight performs every check that can be made without creating anything:
// the file's shape, the caller's identity, and the existence of everything the
// file references. Both --dry-run and a real run start here.
func preflight(ctx context.Context, opts options) (plan, error) {
	accounts, err := inputfile.ParseFile(opts.inputPath)
	if err != nil {
		return plan{}, err
	}

	cfg, err := config.Load()
	if err != nil {
		return plan{}, err
	}

	awsCfg, err := awsauth.Resolve(ctx, awsauth.Options{
		Profile:        opts.profile,
		RootAccountID:  cfg.RootAccountID,
		AssumeRoleName: cfg.AssumeRoleName,
	})
	if err != nil {
		return plan{}, err
	}

	catalog, err := discovery.Load(ctx, discovery.NewDeps(awsCfg))
	if err != nil {
		return plan{}, err
	}

	if err := catalog.Verify(accounts); err != nil {
		return plan{}, err
	}
	return plan{accounts: accounts, catalog: catalog}, nil
}

// printer writes progress to the terminal. Failures there are not actionable,
// so they are deliberately ignored rather than threaded through every caller.
type printer struct{ w io.Writer }

func (p printer) printf(format string, a ...any) {
	_, _ = fmt.Fprintf(p.w, format, a...)
}

func runDryRun(ctx context.Context, opts options, out io.Writer) error {
	p, err := preflight(ctx, opts)
	if err != nil {
		return err
	}
	printPlan(out, p)
	printer{out}.printf("\nDry run complete. Nothing was created.\n")
	return nil
}

func printPlan(out io.Writer, p plan) {
	pr := printer{out}

	assignments := 0
	for _, a := range p.accounts {
		assignments += len(a.Assignments)
	}

	pr.printf("Would create %s with %s:\n\n",
		pluralize(len(p.accounts), "account"), pluralize(assignments, "assignment"))

	for _, account := range p.accounts {
		ouName, _ := p.catalog.OUName(account.OUID)
		pr.printf("  %s (%s)\n", account.Name, account.Email)
		pr.printf("    OU: %s (%s)\n", ouName, account.OUID)
		for _, a := range account.Assignments {
			pr.printf("    %-5s %-24s %s\n", a.PrincipalType, a.PrincipalName, a.PermissionSetName)
		}
		pr.printf("\n")
	}
}

func pluralize(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func runInit(_ options) error      { return errNotImplemented }
func runProvision(_ options) error { return errNotImplemented }
