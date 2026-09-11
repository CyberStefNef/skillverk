// Command skillverk keeps one shared skill library and one selection per Git
// working tree.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/CyberStefNef/skillverk/internal/library"
	"github.com/CyberStefNef/skillverk/internal/tui"
	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"os"
	"runtime/debug"
	"strings"
)

const overview = `Skillverk keeps one shared library of agent skills and one selection per Git
working tree. Importing a skill copies it into the library; turning it on links
it into the harnesses this repository has enabled. The two steps stay separate.

Run skillverk with no arguments to open the interactive picker. Outside a Git
working tree the library commands still work; activation does not.`

// env is the resolved context every command runs in. It is built once, in the
// root command's PersistentPreRunE, so subcommands only describe themselves.
type env struct {
	store *library.Store
	repo  string

	home string
	dir  string
	json bool
	yes  bool

	// pending holds cleanup that reconciliation could not finish, reported
	// before the command's own output.
	pending []library.Result
}

func (e *env) emit(v any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(v)
}

// confirm states the exact effects of a change and requires approval. With
// --json or without a terminal, only an explicit --yes counts.
func (e *env) confirm(message string) error {
	fmt.Fprintln(os.Stderr, library.Clean(message))
	if e.yes {
		return nil
	}
	if e.json || !term.IsTerminal(os.Stdin.Fd()) {
		return errors.New("confirmation required: review the paths and effects above, then repeat with --yes")
	}
	fmt.Fprint(os.Stderr, "Proceed? [y/N] ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(line)) != "y" {
		return errors.New("cancelled")
	}
	return nil
}

// report prints operation results, exposing partial failures alongside the
// changes that did succeed.
func (e *env) report(results []library.Result, err error) error {
	if e.json {
		message := ""
		if err != nil {
			message = err.Error()
		}
		if er := e.emit(map[string]any{"results": results, "ok": err == nil, "error": message}); er != nil {
			return er
		}
		return err
	}
	for _, r := range results {
		fmt.Printf("%s: %s %s %s", r.Action, r.Name, r.Agent, library.Clean(r.Path))
		if r.Error != "" {
			fmt.Print(": " + library.Clean(r.Error))
		}
		fmt.Println()
	}
	return err
}

// reconcile applies any repository changes recorded but not yet completed,
// and reports whatever remains pending.
func (e *env) reconcile() {
	pending, err := e.store.Reconcile(e.repo)
	e.pending = pending
	for _, r := range pending {
		fmt.Fprintln(os.Stderr, library.Clean(fmt.Sprintf("%s: %s %s %s %s", r.Action, r.Name, r.Agent, r.Path, r.Error)))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cleanup remains incomplete:", library.Clean(err.Error()))
	}
}

func newRoot() (*cobra.Command, *env) {
	e := &env{}
	root := &cobra.Command{
		Use:   "skillverk",
		Short: "One shared skill library, one selection per Git working tree",
		Long:  overview,
		Args:  cobra.NoArgs,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			store, err := library.New(e.home)
			if err != nil {
				return err
			}
			repo, err := library.Project(e.dir)
			if err != nil {
				return err
			}
			e.store, e.repo = store, repo
			if cmd.Name() != "setup" {
				e.reconcile()
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if e.json {
				return errors.New("the picker is interactive; use skillverk list --json instead")
			}
			return tui.Run(e.store, e.repo)
		},
	}
	flags := root.PersistentFlags()
	flags.StringVar(&e.home, "home", "", "use an alternate private library (also SKILLVERK_HOME)")
	flags.StringVarP(&e.dir, "directory", "C", "", "resolve the Git working tree from this directory")
	flags.BoolVar(&e.json, "json", false, "produce machine-readable output")
	flags.BoolVarP(&e.yes, "yes", "y", false, "approve the change the command describes")

	root.AddCommand(
		addCommand(e),
		listCommand(e),
		inspectCommand(e),
		selectCommand(e, true),
		selectCommand(e, false),
		agentsCommand(e),
		retryCommand(e),
		updateCommand(e),
		localizeCommand(e),
		adoptCommand(e),
		originalsCommand(e),
		cleanupCommand(e),
		deleteCommand(e),
		doctorCommand(e),
		pathCommand(e),
		setupCommand(e),
	)
	root.SetHelpCommand(&cobra.Command{Hidden: true, Use: "no-help"})
	return root, e
}

// buildVersion is the release tag, set with -ldflags by the release workflow.
// A binary built any other way falls back to what the Go toolchain stamps in.
var buildVersion string

// version describes the running build, so two installs can be told apart. A
// build from a dirty tree says so: during development that is the common case,
// and it is exactly the thing worth knowing when a binary does not behave like
// its source.
func version() string {
	if buildVersion != "" {
		return buildVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	revision := settings["vcs.revision"]
	if revision == "" {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
		return "unknown"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if settings["vcs.modified"] == "true" {
		revision += " (modified)"
	}
	if stamped := settings["vcs.time"]; stamped != "" {
		revision += " " + stamped
	}
	return revision
}

// run executes one command line. main and the tests share this entry point.
func run(args []string) error {
	root, _ := newRoot()
	root.SetArgs(args)
	// fang replaces root.Version with its own, so the build description has to
	// arrive as an option rather than a field.
	return fang.Execute(context.Background(), root,
		fang.WithNotifySignal(os.Interrupt),
		fang.WithVersion(version()),
	)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}
