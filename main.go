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
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/servicecatalog"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"

	"github.com/kawaguchi1035/aws-account-provisioner/internal/assigner"
	"github.com/kawaguchi1035/aws-account-provisioner/internal/awsauth"
	"github.com/kawaguchi1035/aws-account-provisioner/internal/config"
	"github.com/kawaguchi1035/aws-account-provisioner/internal/discovery"
	"github.com/kawaguchi1035/aws-account-provisioner/internal/inputfile"
	"github.com/kawaguchi1035/aws-account-provisioner/internal/provisioner"
	"github.com/kawaguchi1035/aws-account-provisioner/internal/report"
	"github.com/kawaguchi1035/aws-account-provisioner/internal/wizard"
)

// version is overridden at build time via -ldflags.
var version = "dev"

type options struct {
	init        bool
	dryRun      bool
	profile     string
	showVersion bool
	assumeYes   bool
	inputPath   string
}

func main() {
	// A first interrupt cancels the context so polling stops cleanly; a second
	// one restores the default behaviour and kills the process outright.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
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
		return runInit(ctx, opts, out)
	case opts.dryRun:
		return runDryRun(ctx, opts, out)
	default:
		return runProvision(ctx, opts, out)
	}
}

func parseArgs(args []string) (options, error) {
	var opts options

	fs := flag.NewFlagSet("aws-account-provisioner", flag.ContinueOnError)
	fs.BoolVar(&opts.init, "init", false, "launch the interactive wizard and write input.tsv")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "validate input and AWS state without creating anything")
	fs.StringVar(&opts.profile, "profile", "", "AWS profile used to reach the management account")
	fs.BoolVar(&opts.assumeYes, "yes", false, "skip the confirmation prompt before creating accounts")
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
	accounts       []inputfile.Account
	catalog        *discovery.Catalog
	config         config.Config
	serviceCatalog *servicecatalog.Client
	ssoAdmin       *ssoadmin.Client
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
	return plan{
		accounts:       accounts,
		catalog:        catalog,
		config:         cfg,
		serviceCatalog: servicecatalog.NewFromConfig(awsCfg),
		ssoAdmin:       ssoadmin.NewFromConfig(awsCfg),
	}, nil
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

// defaultInputPath is where --init writes its result.
const defaultInputPath = "input.tsv"

func runInit(ctx context.Context, opts options, out io.Writer) error {
	pr := printer{out}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	awsCfg, err := awsauth.Resolve(ctx, awsauth.Options{
		Profile:        opts.profile,
		RootAccountID:  cfg.RootAccountID,
		AssumeRoleName: cfg.AssumeRoleName,
	})
	if err != nil {
		return err
	}

	pr.printf("Reading organizational units, permission sets and principals...\n\n")
	catalog, err := discovery.Load(ctx, discovery.NewDeps(awsCfg))
	if err != nil {
		return err
	}

	prompter := wizard.TerminalPrompter{}
	accounts, err := wizard.Wizard{
		Prompter: prompter,
		Catalog:  catalog,
		Email:    cfg,
		Out:      out,
	}.Run()
	if err != nil {
		return err
	}

	pr.printf("\nThe following will be written to %s:\n", defaultInputPath)
	wizard.Summarize(out, accounts)

	if err := confirmOverwrite(prompter, defaultInputPath); err != nil {
		return err
	}
	if err := inputfile.WriteFile(defaultInputPath, accounts); err != nil {
		return err
	}

	pr.printf("\nWrote %s. Review it, then run:\n  aws-account-provisioner --dry-run %s\n",
		defaultInputPath, defaultInputPath)
	return nil
}

// confirmOverwrite asks before replacing an existing input file, so a wizard
// run cannot silently discard one that is already in use.
func confirmOverwrite(p wizard.Prompter, path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	ok, err := p.Confirm(fmt.Sprintf("%s already exists. Overwrite?", path))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s already exists, nothing was written", path)
	}
	return nil
}

func runProvision(ctx context.Context, opts options, out io.Writer) error {
	pr := printer{out}

	p, err := preflight(ctx, opts)
	if err != nil {
		return err
	}
	printPlan(out, p)

	if err := confirmProvision(opts, len(p.accounts)); err != nil {
		return err
	}

	// Progress arrives from several goroutines at once, so serialise it.
	var progressMu sync.Mutex
	progress := func(accountName, message string) {
		progressMu.Lock()
		defer progressMu.Unlock()
		pr.printf("  [%s] %s\n", accountName, message)
	}

	assign := assigner.Assigner{
		API:         p.ssoAdmin,
		Resolver:    p.catalog,
		InstanceARN: p.catalog.InstanceARN,
		Progress:    progress,
	}

	pr.printf("Creating accounts. This takes 20-40 minutes per account.\n\n")
	results := provisioner.Provisioner{
		API:              p.serviceCatalog,
		ProductID:        p.catalog.AccountFactoryProductID,
		ArtifactID:       p.catalog.AccountFactoryArtifactID,
		SSOUserFirstName: p.config.SSOUserFirstName,
		SSOUserLastName:  p.config.SSOUserLastName,
		Progress:         progress,
		OnCreated:        assign.Assign,
	}.Provision(ctx, p.accounts)

	return reportResults(out, results)
}

// confirmProvision asks before an operation that cannot be undone.
func confirmProvision(opts options, accounts int) error {
	if opts.assumeYes {
		return nil
	}
	ok, err := wizard.TerminalPrompter{}.Confirm(
		fmt.Sprintf("Create %s? This cannot be undone", pluralize(accounts, "account")))
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("cancelled, nothing was created")
	}
	return nil
}

// resultsDir is where the timestamped result file is written.
const resultsDir = "results"

func reportResults(out io.Writer, results []provisioner.Result) error {
	pr := printer{out}

	var succeeded, failed, partial int
	pr.printf("\nResults:\n")
	for _, r := range results {
		switch {
		case !r.Succeeded():
			failed++
			pr.printf("  FAILED   %-24s %v\n", r.Account.Name, r.Err)
		case r.AfterErr != nil:
			partial++
			pr.printf("  PARTIAL  %-24s %s\n", r.Account.Name, r.AccountID)
			pr.printf("           created, but %v\n", r.AfterErr)
		default:
			succeeded++
			pr.printf("  OK       %-24s %s\n", r.Account.Name, r.AccountID)
		}
	}
	pr.printf("\n%d succeeded, %d partial, %d failed\n", succeeded, partial, failed)

	if path, err := report.WriteFile(resultsDir, time.Now(), resultRows(results)); err != nil {
		// Losing the result file must not mask the outcome of the run itself.
		pr.printf("\nWarning: could not write the result file: %v\n", err)
	} else {
		pr.printf("Wrote %s\n", path)
	}

	// Anything still in flight on the AWS side deserves an explicit warning:
	// stopping the tool does not stop Control Tower.
	for _, r := range results {
		if errors.Is(r.Err, provisioner.ErrSubmitted) {
			pr.printf("\nWarning: some accounts were requested but not confirmed. " +
				"Control Tower may still be creating them - check the console before retrying.\n")
			break
		}
	}

	if failed > 0 {
		return fmt.Errorf("%d of %d accounts could not be created", failed, len(results))
	}
	if partial > 0 {
		return fmt.Errorf("%d of %d accounts were created but not fully assigned", partial, len(results))
	}
	return nil
}

func resultRows(results []provisioner.Result) []report.Row {
	rows := make([]report.Row, 0, len(results))
	for _, r := range results {
		row := report.Row{
			OU:           fmt.Sprintf("%s (%s)", r.Account.OUName, r.Account.OUID),
			AccountID:    r.AccountID,
			AccountName:  r.Account.Name,
			AccountEmail: r.Account.Email,
			Status:       report.StatusSucceeded,
		}
		switch {
		case !r.Succeeded():
			row.Status = report.StatusFailed
			row.ErrorMessage = errorText(r.Err)
		case r.AfterErr != nil:
			// The account exists, so it is not a failure, but the problem must
			// still be recorded.
			row.ErrorMessage = errorText(r.AfterErr)
		}
		rows = append(rows, row)
	}
	return rows
}

// errorText flattens an error onto one line so it survives a TSV cell.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.Join(strings.Fields(err.Error()), " ")
}
