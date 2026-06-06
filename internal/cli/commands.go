package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eriklanning/lintel/internal/dsl"
	"github.com/eriklanning/lintel/internal/engine"
	"github.com/eriklanning/lintel/internal/lang"
	"github.com/eriklanning/lintel/internal/runner"
)

// --- test ----------------------------------------------------------------

func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test [rule-ids...]",
		Short: "Run the inline test cases embedded in rules",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rulesDir, _ := cmd.Flags().GetString("rules")
			rs, lerrs, err := runner.Load(".", rulesDir)
			if err != nil {
				return err
			}
			for _, le := range lerrs {
				fmt.Fprintf(os.Stderr, "rule error: %s\n", le.Error())
			}
			want := map[string]bool{}
			for _, a := range args {
				want[a] = true
			}

			total, passed, failed, rulesTested := 0, 0, 0, 0
			for _, r := range rs.Rules() {
				if len(want) > 0 && !want[r.ID] {
					continue
				}
				results := runner.RunRuleTests(r)
				if len(results) == 0 {
					continue
				}
				rulesTested++
				for _, cr := range results {
					total++
					if cr.Pass {
						passed++
						continue
					}
					failed++
					fmt.Printf("%s  %s case #%d: %s\n",
						colorize(!flagNoColor(cmd), red, "FAIL"), r.ID, cr.Index+1, describeCase(cr))
				}
			}
			if total == 0 {
				fmt.Println("no test cases found (add a test { ... } block to a rule)")
				return nil
			}
			fmt.Printf("\n%d rule(s), %d case(s): %d passed, %d failed\n", rulesTested, total, passed, failed)
			if failed > 0 || len(lerrs) > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().String("rules", "", "directory of .lint rules (default: discover .lintel/)")
	cmd.Flags().Bool("no-color", false, "disable colored output")
	return cmd
}

func describeCase(cr runner.CaseResult) string {
	switch cr.Expect {
	case dsl.ExpectMatch:
		if cr.Want >= 0 {
			return fmt.Sprintf("expected %d match(es), got %d", cr.Want, cr.Got)
		}
		return fmt.Sprintf("expected a match, got %d", cr.Got)
	default:
		return fmt.Sprintf("expected no match, got %d", cr.Got)
	}
}

// --- list ----------------------------------------------------------------

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List discovered rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			rulesDir, _ := cmd.Flags().GetString("rules")
			rs, lerrs, err := runner.Load(".", rulesDir)
			if err != nil {
				return err
			}
			for _, le := range lerrs {
				fmt.Fprintf(os.Stderr, "rule error: %s\n", le.Error())
			}
			rules := rs.Rules()
			if len(rules) == 0 {
				fmt.Println("no rules found.")
				return nil
			}
			for _, r := range rules {
				var langs []string
				for _, l := range r.Languages() {
					langs = append(langs, l.Name)
				}
				sort.Strings(langs)
				fmt.Printf("%-28s %-8s [%s]\n    %s\n",
					r.ID, r.Severity, strings.Join(langs, ", "), r.Message)
			}
			return nil
		},
	}
	cmd.Flags().String("rules", "", "directory of .lint rules (default: discover .lintel/)")
	return cmd
}

// --- parse ---------------------------------------------------------------

func newParseCmd() *cobra.Command {
	var langName, pattern string
	cmd := &cobra.Command{
		Use:   "parse [file]",
		Short: "Print the syntax tree of a file, or how a pattern compiles",
		Long: `parse helps author rules.

  lintel parse path/to/file.go              # print the file's syntax tree
  lintel parse --lang go --pattern 'a + b'  # show how a pattern resolves`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if pattern != "" {
				if langName == "" {
					return fmt.Errorf("--pattern requires --lang")
				}
				l, ok := lang.Get(langName)
				if !ok {
					return fmt.Errorf("unknown language %q", langName)
				}
				out, err := engine.DebugPattern(l, pattern)
				if err != nil {
					return err
				}
				fmt.Println(out)
				return nil
			}
			if len(args) == 0 {
				return fmt.Errorf("provide a file, or --lang and --pattern")
			}
			path := args[0]
			l, ok := lang.ForPath(path)
			if langName != "" {
				l, ok = lang.Get(langName)
			}
			if !ok {
				return fmt.Errorf("could not determine language for %q (use --lang)", path)
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			tree, err := engine.ParseSource(l, src)
			if err != nil {
				return err
			}
			fmt.Println(tree.RootNode().String())
			return nil
		},
	}
	cmd.Flags().StringVar(&langName, "lang", "", "language to use")
	cmd.Flags().StringVar(&pattern, "pattern", "", "a pattern to compile and inspect")
	return cmd
}

// --- new -----------------------------------------------------------------

func newNewCmd() *cobra.Command {
	var langName, dir string
	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Scaffold a new .lint rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if dir == "" {
				dir = ".lintel"
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			path := filepath.Join(dir, name+".lint")
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("%s already exists", path)
			}
			inLine := langName
			if inLine == "" {
				inLine = "go"
			}
			tmpl := fmt.Sprintf(`rule %s {
  message  "Describe what's wrong and what to do instead"
  severity warning
  in       %s

  pattern { yourFunction($$$ARGS) }

  # where    $ARGS matches "..."
  # fix      "betterFunction($$$ARGS)"

  test {
    match    "yourFunction(1, 2)"
    no_match "betterFunction(1, 2)"
  }
}
`, name, inLine)
			if err := os.WriteFile(path, []byte(tmpl), 0o644); err != nil {
				return err
			}
			fmt.Printf("created %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&langName, "lang", "", "language for the rule's `in` clause")
	cmd.Flags().StringVar(&dir, "dir", "", "directory to create the rule in (default: .lintel)")
	return cmd
}

// --- langs ---------------------------------------------------------------

func newLangsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "langs",
		Short: "List supported languages and file extensions",
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, name := range lang.Names() {
				l, _ := lang.Get(name)
				fmt.Printf("%-12s %s\n", name, strings.Join(l.Extensions, " "))
			}
			return nil
		},
	}
}

// --- helpers -------------------------------------------------------------

func flagNoColor(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("no-color")
	return v
}

func colorize(on bool, code, text string) string {
	if !on {
		return text
	}
	return code + text + "\033[0m"
}

const red = "\033[31m"
