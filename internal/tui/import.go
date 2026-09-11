package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"errors"
	"fmt"
	"github.com/CyberStefNef/skillverk/internal/library"
	"path/filepath"
	"slices"
	"strings"
)

// collectionMsg carries a loaded import source back to the import screen.
type collectionMsg struct {
	id  uint64
	c   *library.Collection
	err error
}

// sourcePage picks which skills to import from a loaded source. Selections
// are keyed by name, so narrowing the search never loses them.
func newSourcePage(a *app, c *library.Collection) *listPage {
	chosen := map[string]bool{}
	p := newList(a, "Choose skills", library.Clean(c.Source), true, true)
	p.detail = func(a *app, p *listPage, width int) []string {
		r, ok := p.focus()
		if !ok {
			return nil
		}
		sk := r.data.(library.Skill)
		lines := []string{a.theme.Heading.Render(sk.Name), ""}
		lines = append(lines, field(a.theme, "Source", library.Clean(c.Source), width)...)
		lines = append(lines, field(a.theme, "Path", library.Clean(sk.Path), width)...)
		lines = append(lines, "")
		lines = append(lines, wrap(a.theme.Muted, library.Clean(sk.Description), width, 6)...)
		return append(lines, "", a.theme.Muted.Render("Space selects. Enter imports selected skills."))
	}
	p.closer = c.Close
	p.keys = []key.Binding{keys.All, bind("v", "details")}
	if len(c.Skipped) > 0 {
		p.keys = append(p.keys, bind("w", "skipped entries"))
	}

	inLibrary := map[string]bool{}
	for _, sk := range a.data.skills {
		if sk.Entry != nil {
			inLibrary[sk.Name] = true
		}
	}
	fill := func() {
		var rows []row
		for _, sk := range c.Skills {
			check := checkOff
			if chosen[sk.Name] {
				check = checkOn
			}
			badge := ""
			if inLibrary[sk.Name] {
				badge = "already in library"
			}
			rows = append(rows, row{id: sk.Name, name: sk.Name, desc: sk.Description, badge: badge, check: check, data: sk})
		}
		p.setRows(rows)
	}
	fill()

	p.toggle = func(a *app, p *listPage) tea.Cmd {
		if r, ok := p.focus(); ok {
			chosen[r.id] = !chosen[r.id]
			fill()
		}
		return nil
	}
	p.other = func(a *app, p *listPage, k tea.KeyPressMsg) tea.Cmd {
		switch k.String() {
		case "a":
			visible := p.list.VisibleItems()
			all := len(visible) > 0
			for _, item := range visible {
				all = all && chosen[item.(row).id]
			}
			for _, item := range visible {
				chosen[item.(row).id] = !all
			}
			fill()
			return nil
		case "v":
			r, ok := p.focus()
			if !ok {
				return nil
			}
			sk := r.data.(library.Skill)
			return push(newDocPage(sk.Name, "From "+library.Clean(c.Source), sk.Description))
		case "w":
			if len(c.Skipped) == 0 {
				return nil
			}
			body := "Valid skills in this source can still be imported.\n"
			for _, issue := range c.Skipped {
				body += "\n" + library.Clean(issue.Path) + "\n    " + library.Clean(issue.Error) + "\n"
			}
			return push(newDocPage("Skipped entries", "", body))
		}
		return nil
	}
	p.notes = func(a *app, p *listPage) []string {
		lines := []string{a.theme.Muted.Render(fmt.Sprintf("%d selected of %d", countChosen(chosen), len(c.Skills)))}
		if len(c.Skipped) > 0 {
			lines = append(lines, a.theme.Warn.Render(fmt.Sprintf("! %d invalid entries skipped; press w to read why", len(c.Skipped))))
		}
		return lines
	}
	p.enter = func(a *app, p *listPage) tea.Cmd {
		names := chosenNames(chosen)
		if len(names) == 0 {
			return warn("Select at least one skill with Space.")
		}
		return importSkills(a, c, names, false)
	}
	return p
}

// importSkills publishes the chosen entries, turning a name conflict into an
// explicit replacement question rather than a bare failure.
func importSkills(a *app, c *library.Collection, names []string, replace bool) tea.Cmd {
	a.jobDone = func(a *app, v jobMsg) tea.Cmd {
		var conflict *library.ConflictError
		if errors.As(v.err, &conflict) {
			body := library.Clean(conflict.Error()) + "\n\nReplacing changes the shared content in every repository that has these skills turned on."
			return push(newConfirmPage("Replace existing skills", "Replace", body, func(a *app) tea.Cmd {
				return importSkills(a, c, names, true)
			}))
		}
		return offerToTurnOn(a, v)
	}
	return a.perform("Importing", func() ([]library.Result, error) {
		entries, err := a.store.Publish(c, names, replace)
		var results []library.Result
		for name, entry := range entries {
			results = append(results, library.Result{Action: "imported", Name: name, Path: entry.Subpath})
			for _, n := range entry.Requirements.Notes {
				results = append(results, library.Result{Action: "note", Name: name, Path: n})
			}
			if paths := entry.Requirements.GlobalPaths; len(paths) > 0 {
				results = append(results, library.Result{Action: "requires global paths", Name: name, Path: strings.Join(paths, ", ")})
			}
			if plugins := entry.Requirements.Plugins; len(plugins) > 0 {
				results = append(results, library.Result{Action: "requires plugins", Name: name, Path: strings.Join(plugins, ", ")})
			}
		}
		slices.SortFunc(results, func(x, y library.Result) int { return strings.Compare(x.Name, y.Name) })
		return results, err
	})
}

// offerToTurnOn asks the question an import leaves hanging. A skill in the
// library does nothing until a repository links it, and the moment someone has
// just chosen it by name is the moment they know whether they want it here.
// Answering no is free: the copy stays, and space on the list turns it on later.
func offerToTurnOn(a *app, v jobMsg) tea.Cmd {
	imported := importedNames(v.results)
	if v.err != nil || a.repo == "" || len(imported) == 0 {
		return nil // the ordinary report says what happened
	}
	if err := a.refresh(); err != nil {
		return failure(err)
	}
	body := strings.Join([]string{
		"Imported " + strings.Join(imported, ", ") + ".",
		"",
		"Turning them on links them into " + filepath.Base(a.repo) + ". They stay in",
		"your library either way.",
	}, "\n")
	question := newQuestionPage("Turn them on here?", "Turn on", "Not now", body, func(a *app) tea.Cmd {
		return tea.Sequence(toRoot, a.perform("Turning on", func() ([]library.Result, error) {
			return a.store.Select(a.repo, imported, true)
		}))
	})
	return tea.Sequence(toRoot, reload, func() tea.Msg {
		return noteMsg{text: summarize(v.results), detail: resultLines(v.results, nil), lvl: levelDone}
	}, push(question))
}

// importedNames are the skills an import actually added.
func importedNames(results []library.Result) []string {
	var out []string
	for _, r := range results {
		if r.Action == "imported" {
			out = append(out, r.Name)
		}
	}
	return out
}

func countChosen(chosen map[string]bool) int {
	n := 0
	for _, on := range chosen {
		if on {
			n++
		}
	}
	return n
}

func chosenNames(chosen map[string]bool) []string {
	var names []string
	for name, on := range chosen {
		if on {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
