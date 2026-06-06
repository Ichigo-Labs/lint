// Package cli wires the lint command-line interface.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// defaultVersion is used for un-stamped builds (a plain `go build`); release
// builds override `version` via -ldflags. See version.go for how commit and
// build date are resolved.
const defaultVersion = "0.1.0-dev"

// version is overridden at build time with -ldflags.
var version = defaultVersion

// Execute runs the lint CLI.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "lint:", err)
		os.Exit(2)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "lint [paths...]",
		Short: "lint — write project lint rules fast, run them across many languages",
		Long: `lint finds rule violations in your code using structural patterns.

Rules live in a .lint/ directory at the project root as NAME.lint files written
in a small DSL that matches code by its syntax tree (not regex), across C, C++,
C#, Go, Java, Kotlin, Python, Rust, Swift, and TypeScript/JavaScript.

Running 'lint' with no subcommand checks the current directory.`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(cmd, args)
		},
		// `lint --version` prints a one-line summary; `lint version` prints the
		// full build details.
		Version: readBuildInfo().short(),
	}
	addCheckFlags(root)

	root.AddCommand(newCheckCmd())
	root.AddCommand(newTestCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newParseCmd())
	root.AddCommand(newNewCmd())
	root.AddCommand(newLangsCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newInitCmd())
	return root
}
