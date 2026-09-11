package main

import (
	"errors"
	"fmt"
	"github.com/CyberStefNef/skillverk/internal/library"
	"github.com/CyberStefNef/skillverk/internal/onboarding"
	"github.com/CyberStefNef/skillverk/internal/tui"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"slices"
)

func setupCommand(e *env) *cobra.Command {
	var (
		scan      bool
		companion bool
		plan      string
		apply     string
		provider  string
	)
	cmd := &cobra.Command{
		Use:   "setup [ROOT...]",
		Short: "Review the skills already installed on this machine",
		Long: `Find existing skill installations and decide what happens to each one.

With no options this opens the interactive review. --scan previews the plan
without touching anything, --plan writes that plan to a file, and --apply
carries out a plan you have already reviewed.

ROOT limits the search to specific directories. The default roots are the
common project directories under your home directory.`,
		Example: "  skillverk setup\n  skillverk setup --scan --json ~/code\n  skillverk setup --scan --plan plan.json && skillverk setup --apply plan.json --yes",
		RunE: func(cmd *cobra.Command, roots []string) error {
			ctx := cmd.Context()
			switch {
			case provider != "" && scan:
				return errors.New("choose provider review or --scan preview, not both")
			case plan != "" && (!scan || apply != "" || provider != ""):
				return errors.New("--plan requires --scan, without --apply or --provider")
			case provider != "" && !slices.Contains([]string{"auto", "codex", "claude", "local"}, provider):
				return errors.New("choose auto, codex, claude, or local")
			case provider != "" && !interactiveTerminal():
				return errors.New("provider review requires an interactive terminal")
			}
			if companion {
				if scan || apply != "" || provider != "" || len(roots) > 0 {
					return errors.New("use --install-companion by itself")
				}
				paths, err := onboarding.Install()
				if err != nil {
					return err
				}
				if e.json {
					return e.emit(paths)
				}
				for _, p := range paths {
					fmt.Println(p)
				}
				return nil
			}
			if apply != "" {
				if scan || provider != "" || len(roots) > 0 {
					return errors.New("--apply cannot be combined with scanning or provider launch")
				}
				saved, err := library.ReadSetupPlan(apply)
				if err != nil {
					return err
				}
				if err := e.confirm(saved.Summary()); err != nil {
					return err
				}
				results, err := e.store.ApplySetup(ctx, saved, true)
				if err == nil {
					err = e.store.MarkSetupReviewed()
				}
				return e.report(results, err)
			}
			// local is the stored setting's name for reviewing in Skillverk,
			// which is what an omitted provider already does.
			if provider == "local" {
				provider = ""
			}
			if provider == "auto" {
				if provider = onboarding.Resolve("auto"); provider == "local" {
					provider = ""
				}
			}
			if !scan && provider == "" && !e.json {
				return tui.Setup(e.store, e.repo, roots)
			}
			if provider != "" {
				if e.json {
					return errors.New("provider review needs an interactive terminal; omit --json")
				}
				settings, err := e.store.ReviewSettings()
				if err != nil {
					return err
				}
				launch, err := onboarding.StartReview(provider, settings.Models[provider], e.store, e.repo, roots)
				if err != nil {
					return err
				}
				launch.Stdin, launch.Stdout, launch.Stderr = os.Stdin, os.Stdout, os.Stderr
				return launch.Run()
			}
			if len(roots) == 0 {
				roots = library.DefaultSetupRoots()
			}
			scanned, err := e.store.ScanSetup(ctx, roots)
			if err != nil {
				return err
			}
			if plan != "" {
				path, err := filepath.Abs(plan)
				if err != nil {
					return err
				}
				if err := library.SaveSetupPlan(path, scanned); err != nil {
					return err
				}
			}
			if e.json {
				return e.emit(scanned)
			}
			fmt.Println(library.Clean(scanned.Summary()))
			return nil
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&scan, "scan", false, "preview what setup would change, without changing it")
	flags.StringVar(&plan, "plan", "", "write the scanned plan to this file")
	flags.StringVar(&apply, "apply", "", "apply a plan file you have already reviewed")
	flags.StringVar(&provider, "provider", "", "review through an installed CLI: auto, codex, claude, or local")
	flags.BoolVar(&companion, "install-companion", false, "install the optional setup skill")
	cmd.RegisterFlagCompletionFunc("provider", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"auto", "codex", "claude", "local"}, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func interactiveTerminal() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}
