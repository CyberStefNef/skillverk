package main

import (
	"errors"
	"fmt"
	"github.com/CyberStefNef/skillverk/internal/library"
	"github.com/CyberStefNef/skillverk/internal/tui"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"os"
	"slices"
	"strings"
)

// skillNames completes skill names from the library and this repository.
func skillNames(e *env) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		rows, err := e.store.Catalog(e.repo)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var out []string
		for _, sk := range rows {
			if strings.HasPrefix(sk.Name, prefix) {
				out = append(out, sk.Name+"\t"+library.Clean(sk.Description))
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

func addCommand(e *env) *cobra.Command {
	var (
		names   []string
		all     bool
		listing bool
		replace bool
	)
	cmd := &cobra.Command{
		Use:   "add SOURCE",
		Short: "Import skills from a folder, archive, or source URL",
		Long: `Copy skills into the shared library. A source can be a local directory, an
archive, a GitHub shorthand such as owner/repo, or a URL to a subdirectory.

Importing never turns a skill on: use skillverk on afterwards.

With no --skill or --all and more than one skill in the source, this opens the
interactive picker.`,
		Example: "  skillverk add mattpocock/skills\n  skillverk add ./my-skill\n  skillverk add owner/repo --skill review --skill lint",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := library.OpenCollection(args[0])
			if err != nil {
				return err
			}
			defer c.Close()
			for _, issue := range c.Skipped {
				fmt.Fprintln(os.Stderr, library.Clean("Skipped "+issue.Path+": "+issue.Error))
			}
			if listing {
				if e.json {
					return e.emit(c.Skills)
				}
				for _, sk := range c.Skills {
					fmt.Println(sk.Name, library.Clean(sk.Description))
				}
				return nil
			}
			var chosen []string
			for _, name := range names {
				chosen = append(chosen, strings.Split(name, ",")...)
			}
			if all {
				for _, sk := range c.Skills {
					chosen = append(chosen, sk.Name)
				}
			}
			if len(chosen) == 0 && len(c.Skills) > 1 && !e.json && term.IsTerminal(os.Stdin.Fd()) {
				return tui.Browse(e.store, e.repo, c)
			}
			selected, err := c.Select(chosen)
			if err != nil {
				return err
			}
			if err := confirmReplacements(e, c, selected, replace); err != nil {
				return err
			}
			entries, err := e.store.Publish(c, chosen, replace)
			if e.json {
				return errors.Join(err, e.emit(map[string]any{"imported": entries, "ok": err == nil}))
			}
			for name, entry := range entries {
				fmt.Println("Imported", name+".", "Turn it on with: skillverk on", name)
				for _, note := range entry.Requirements.Notes {
					fmt.Println(" ", library.Clean(note))
				}
				if paths := entry.Requirements.GlobalPaths; len(paths) > 0 {
					fmt.Println("  Requires global paths:", strings.Join(paths, ", "))
				}
				if plugins := entry.Requirements.Plugins; len(plugins) > 0 {
					fmt.Println("  Requires plugin support:", strings.Join(plugins, ", "))
				}
			}
			return err
		},
	}
	cmd.Flags().StringArrayVar(&names, "skill", nil, "import only this entry; repeat or comma-separate")
	cmd.Flags().BoolVar(&all, "all", false, "import every entry in the source")
	cmd.Flags().BoolVar(&listing, "list", false, "list the source without importing")
	cmd.Flags().BoolVar(&replace, "replace", false, "replace library content that already uses these names")
	cmd.MarkFlagsMutuallyExclusive("all", "skill")
	cmd.MarkFlagsMutuallyExclusive("list", "all")
	cmd.MarkFlagsMutuallyExclusive("list", "skill")
	return cmd
}

// confirmReplacements makes replacing existing library content deliberate,
// because it changes the content every activated repository sees.
func confirmReplacements(e *env, c *library.Collection, selected []library.Skill, replace bool) error {
	var conflicts []string
	for _, sk := range selected {
		old, err := e.store.Find("", sk.Name)
		if err != nil || old.Entry == nil {
			continue
		}
		conflicts = append(conflicts, fmt.Sprintf("%s\n  existing: %s / %s\n  incoming: %s / %s",
			sk.Name, old.Entry.Source, old.Entry.Subpath, c.Source, sk.Path))
	}
	if len(conflicts) == 0 {
		return nil
	}
	message := strings.Join(conflicts, "\n") + "\nReplacement changes shared content in every activated repository."
	if !replace {
		return errors.New(message + "\nUse --replace for explicit replacement.")
	}
	return e.confirm(message)
}

func listCommand(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show library, repository, and external skills",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			rows, err := e.store.Catalog(e.repo)
			if err != nil {
				return err
			}
			state, err := library.ReadState(e.repo)
			if err != nil {
				return err
			}
			if e.json {
				return e.emit(map[string]any{"repository": e.repo, "agents": state.Agents, "skills": rows, "cleanup": e.pending})
			}
			if e.repo == "" {
				fmt.Println("Library only (outside a Git working tree)")
			} else {
				fmt.Println("Repository:", e.repo, "· harnesses:", strings.Join(state.Agents, ", "))
			}
			active := false
			for _, sk := range rows {
				active = active || sk.Selected || sk.HasScope("project")
			}
			if e.repo != "" && !active {
				fmt.Println("\nRepository skills\nNone selected.")
			}
			section := ""
			for _, sk := range rows {
				next := "Other library / external skills"
				if sk.Selected || sk.HasScope("project") {
					next = "Repository skills"
				}
				if next != section {
					fmt.Println("\n" + next)
					section = next
				}
				fmt.Printf("%-28s %-12s %-10s %s\n", sk.Name, sk.Ownership(), sk.Status(), library.Clean(sk.Description))
				for _, agent := range library.Agents {
					if v := sk.States[agent]; strings.HasPrefix(v, "failed") || strings.HasPrefix(v, "conflict") || v == "missing" {
						fmt.Println(" ", agent+":", library.Clean(v))
					}
				}
			}
			fmt.Println("\nExternal, global, and plugin availability is controlled by whatever installed it.")
			return nil
		},
	}
}

func inspectCommand(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect NAME",
		Short: "Show one skill's sources, harness state, and paths",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			sk, err := e.store.Find(e.repo, args[0])
			if err != nil {
				return err
			}
			if e.json {
				return e.emit(sk)
			}
			fmt.Println(sk.Details())
			return nil
		},
	}
	cmd.ValidArgsFunction = skillNames(e)
	return cmd
}

// selectCommand builds both on and off, which differ only in direction.
func selectCommand(e *env, on bool) *cobra.Command {
	use, short := "on NAME...", "Turn skills on for this repository"
	long := "Changes apply immediately to every harness this repository has enabled.\nA running agent may need to reload before it sees them.\n\n--harness narrows one skill to some of those harnesses; repeat it or pass a\ncomma-separated list. Without it, the skill reaches every enabled harness."
	if !on {
		use, short = "off NAME...", "Turn skills off for this repository"
		long = "Changes apply immediately to every harness this repository has enabled.\nA running agent may need to reload before it sees them.\n\n--harness turns the skill off for those harnesses only, leaving the rest linked.\nTurning off the last one turns the skill off entirely."
	}
	var harnesses []string
	cmd := &cobra.Command{
		Use:     use,
		Short:   short,
		Long:    long,
		Example: "  skillverk " + strings.Fields(use)[0] + " review\n  skillverk " + strings.Fields(use)[0] + " review --harness codex",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			chosen, err := splitAgents(harnesses)
			if err != nil {
				return err
			}
			if len(chosen) == 0 {
				return e.report(e.store.Select(e.repo, args, on))
			}
			state, err := library.ReadState(e.repo)
			if err != nil {
				return err
			}
			var results []library.Result
			for _, name := range args {
				wanted := chosen
				if !on {
					wanted = nil
					for _, agent := range state.SkillAgents(name) {
						if !slices.Contains(chosen, agent) {
							wanted = append(wanted, agent)
						}
					}
				} else {
					wanted = append(append([]string{}, state.SkillAgents(name)...), chosen...)
				}
				rs, err := e.store.SelectHarnesses(e.repo, name, wanted)
				results = append(results, rs...)
				if err != nil {
					return e.report(results, err)
				}
			}
			return e.report(results, nil)
		},
	}
	cmd.Flags().StringSliceVar(&harnesses, "harness", nil, "limit the change to these harnesses")
	_ = cmd.RegisterFlagCompletionFunc("harness", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return library.Agents, cobra.ShellCompDirectiveNoFileComp
	})
	cmd.ValidArgsFunction = skillNames(e)
	return cmd
}

// splitAgents validates harness names given as repeated or comma-separated
// flags, so "--harness codex,claude" and "--harness codex --harness claude"
// mean the same thing.
func splitAgents(values []string) ([]string, error) {
	var out []string
	for _, value := range values {
		for _, name := range strings.Split(value, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if !slices.Contains(library.Agents, name) {
				return nil, fmt.Errorf("unknown harness %s", name)
			}
			out = append(out, name)
		}
	}
	return out, nil
}

func agentsCommand(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "agents [NAME...]",
		Short: "Show or change the harnesses this repository links into",
		Long: `With no arguments, list the enabled and available harnesses.

With arguments, replace the enabled set. Accepts comma-separated lists, "both"
for the defaults, and "all" for every supported harness.`,
		Example: "  skillverk agents\n  skillverk agents claude\n  skillverk agents opencode,cursor gemini",
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return append(library.Agents, "both", "all"), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				state, err := library.ReadState(e.repo)
				if err != nil {
					return err
				}
				if e.json {
					return e.emit(map[string]any{"enabled": state.Agents, "available": library.Agents})
				}
				fmt.Println("Enabled:  ", strings.Join(state.Agents, ", "))
				fmt.Println("Available:", strings.Join(library.Agents, ", "))
				return nil
			}
			var agents []string
			for _, arg := range args {
				agents = append(agents, strings.Split(arg, ",")...)
			}
			if len(agents) == 1 && agents[0] == "both" {
				agents = library.DefaultAgents
			}
			if len(agents) == 1 && agents[0] == "all" {
				agents = library.Agents
			}
			return e.report(e.store.SetAgents(e.repo, agents))
		},
	}
}

func retryCommand(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "retry",
		Short: "Reapply this repository's intended state after a failure",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return e.report(e.store.Retry(e.repo)) },
	}
}

func updateCommand(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update [NAME...]",
		Short: "Refresh shared content from each skill's recorded source",
		Long:  "Refreshing replaces the current content everywhere the skill is active.\nWith no names, every library skill is refreshed.",
		RunE:  func(_ *cobra.Command, args []string) error { return e.report(e.store.Refresh(args)) },
	}
	cmd.ValidArgsFunction = skillNames(e)
	return cmd
}

func localizeCommand(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "localize NAME",
		Short: "Make a Git-tracked, repository-owned copy of a skill",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			message := "Make " + args[0] + " repository-owned in " + e.repo + "?\n" +
				"Stage the skill content and its native links in Git. This repository stops\n" +
				"following shared updates; the library and other repositories are unchanged."
			if err := e.confirm(message); err != nil {
				return err
			}
			return e.report(e.store.Localize(e.repo, args[0], true))
		},
	}
	cmd.ValidArgsFunction = skillNames(e)
	return cmd
}

func adoptCommand(e *env) *cobra.Command {
	var replace bool
	cmd := &cobra.Command{
		Use:   "adopt PATH",
		Short: "Move an existing standalone installation into the library",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if replace {
				c, err := library.OpenCollection(args[0])
				if err != nil {
					return err
				}
				defer c.Close()
				if len(c.Skills) != 1 {
					return errors.New("adopt one standalone skill directory")
				}
				if old, err := e.store.Find("", c.Skills[0].Name); err == nil && old.Entry != nil {
					message := "Existing source: " + old.Entry.Source + "\nIncoming: " + c.Source +
						"\nReplacement changes every activation."
					if err := e.confirm(message); err != nil {
						return err
					}
				}
			}
			message := "Migrate and completely remove the original: " + args[0] + "\n" +
				"Move it into the shared library, replace project originals with managed links,\n" +
				"then delete the saved originals. Git-tracked originals are removed from tracking\n" +
				"and their deletion is staged. Global removal affects other repositories.\n" +
				"Declining leaves the originals and Git tracking untouched."
			if err := e.confirm(message); err != nil {
				return err
			}
			return e.report(e.store.Migrate(e.repo, []string{args[0]}, replace, true))
		},
	}
	cmd.Flags().BoolVar(&replace, "replace", false, "replace library content that already uses this name")
	return cmd
}

func originalsCommand(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "originals [NAME]",
		Short: "Show originals retained after a migration",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			originals, err := e.store.Originals(name)
			if err != nil {
				return err
			}
			if e.json {
				return e.emit(originals)
			}
			for _, o := range originals {
				fmt.Printf("%s  %s\n  original:    %s\n  retained at: %s\n", o.ID, o.Scope, o.Path, o.Backup)
			}
			return nil
		},
	}
}

func cleanupCommand(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup ID",
		Short: "Remove one original retained after a migration",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			originals, err := e.store.Originals("")
			if err != nil {
				return err
			}
			for _, o := range originals {
				if o.ID != args[0] {
					continue
				}
				message := "Remove original " + o.Path
				if o.Backup != "" {
					message += "\nRetained at: " + o.Backup
				}
				if o.Scope == "global" {
					message += "\nGlobal removal affects repositories Skillverk has never seen."
				}
				if err := e.confirm(message); err != nil {
					return err
				}
				result, err := e.store.CleanupOriginal(o.ID, true)
				return e.report([]library.Result{result}, err)
			}
			return errors.New("unknown cleanup ID; run skillverk originals to list them")
		},
	}
}

func deleteCommand(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete NAME...",
		Short: "Delete skills from the library and clean up their links",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var paths []string
			for _, name := range args {
				path, err := e.store.Path(name)
				if err != nil {
					return err
				}
				paths = append(paths, name+": "+path)
			}
			message := "Delete these shared skills and remove every reachable recorded activation:\n" +
				strings.Join(paths, "\n") + "\nUnreachable paths remain pending."
			if err := e.confirm(message); err != nil {
				return err
			}
			return e.report(e.store.DeleteMany(args, true))
		},
	}
	cmd.ValidArgsFunction = skillNames(e)
	return cmd
}

func doctorCommand(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report partial state and pending cleanup",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return e.report(e.store.Doctor(e.repo)) },
	}
}

func pathCommand(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "path [NAME]",
		Short: "Print where library content is stored",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := e.store.SkillsPath()
			if len(args) == 1 {
				var err error
				if path, err = e.store.Path(args[0]); err != nil {
					return err
				}
			}
			fmt.Println(path)
			return nil
		},
	}
	cmd.ValidArgsFunction = skillNames(e)
	return cmd
}
