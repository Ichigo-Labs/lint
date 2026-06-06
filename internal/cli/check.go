package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ichigo-labs/lintel/internal/dsl"
	"github.com/ichigo-labs/lintel/internal/engine"
	"github.com/ichigo-labs/lintel/internal/fix"
	"github.com/ichigo-labs/lintel/internal/lang"
	"github.com/ichigo-labs/lintel/internal/report"
	"github.com/ichigo-labs/lintel/internal/runner"
)

type checkFlags struct {
	rulesDir       string
	jsonOut        bool
	noColor        bool
	applyFix       bool
	quiet          bool
	langs          []string
	jobs           int
	errorOnWarning bool
}

func addCheckFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String("rules", "", "directory of .lint rules (default: discover .lintel/ dirs)")
	f.Bool("json", false, "emit findings as JSON")
	f.Bool("no-color", false, "disable colored output")
	f.Bool("fix", false, "apply autofixes to the working tree")
	f.BoolP("quiet", "q", false, "only show error-severity findings")
	f.StringSlice("lang", nil, "restrict to these languages (e.g. go,typescript)")
	f.IntP("jobs", "j", 0, "number of files to check in parallel (default: CPU count)")
	f.Bool("error-on-warning", false, "exit non-zero if any warnings are found")
}

func readCheckFlags(cmd *cobra.Command) checkFlags {
	f := cmd.Flags()
	rules, _ := f.GetString("rules")
	jsonOut, _ := f.GetBool("json")
	noColor, _ := f.GetBool("no-color")
	applyFix, _ := f.GetBool("fix")
	quiet, _ := f.GetBool("quiet")
	langs, _ := f.GetStringSlice("lang")
	jobs, _ := f.GetInt("jobs")
	eow, _ := f.GetBool("error-on-warning")
	return checkFlags{rules, jsonOut, noColor, applyFix, quiet, langs, jobs, eow}
}

func newCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "check [paths...]",
		Short:         "Check files against discovered rules",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runCheck,
	}
	addCheckFlags(cmd)
	return cmd
}

func runCheck(cmd *cobra.Command, args []string) error {
	flags := readCheckFlags(cmd)

	root := "."
	paths := args
	if len(paths) == 0 {
		paths = []string{"."}
	}

	rs, lerrs, err := runner.Load(root, flags.rulesDir)
	if err != nil {
		return err
	}
	for _, le := range lerrs {
		fmt.Fprintf(os.Stderr, "rule error: %s\n", le.Error())
	}
	if rs.Len() == 0 {
		fmt.Fprintln(os.Stderr, "no rules found (looked for .lintel/*.lint). Run 'lintel new <name>' to create one.")
		if len(lerrs) > 0 {
			os.Exit(2)
		}
		return nil
	}

	// Resolve --lang values (which may be aliases) to canonical names.
	langFilter := map[string]bool{}
	for _, name := range flags.langs {
		if l, ok := lang.Get(name); ok {
			langFilter[l.Name] = true
		} else {
			fmt.Fprintf(os.Stderr, "warning: unknown language %q for --lang\n", name)
		}
	}

	res, err := rs.Check(runner.Options{
		Paths:       paths,
		Concurrency: flags.jobs,
		LangFilter:  langFilter,
	})
	if err != nil {
		return err
	}
	for _, fe := range res.FileErrs {
		fmt.Fprintf(os.Stderr, "warning: %s\n", fe.Error())
	}

	// Apply fixes from the full finding set before any display filtering.
	if flags.applyFix {
		fr, err := fix.Apply(res.Findings, res.Sources)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "applied %d fix(es) across %d file(s)\n", fr.FixesApplied, fr.FilesChanged)
	}

	// The exit decision uses the full finding set, independent of --quiet.
	counts := report.Tally(res.Findings)

	// Build a separate display set; never alias res.Findings' backing array.
	display := res.Findings
	if flags.quiet {
		display = make([]engine.Finding, 0, len(res.Findings))
		for _, f := range res.Findings {
			if f.Severity == dsl.Error {
				display = append(display, f)
			}
		}
	}

	if flags.jsonOut {
		if err := report.JSON(os.Stdout, display); err != nil {
			return err
		}
	} else {
		style := report.Style{Color: useColor(flags.noColor)}
		report.Pretty(os.Stdout, display, res.Sources, style)
	}

	// A malformed rule file is a configuration error (consistent with `test`).
	if len(lerrs) > 0 {
		os.Exit(2)
	}
	if counts.Errors > 0 || (flags.errorOnWarning && counts.Warnings > 0) {
		os.Exit(1)
	}
	return nil
}

// useColor reports whether ANSI color should be emitted.
func useColor(noColor bool) bool {
	if noColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
