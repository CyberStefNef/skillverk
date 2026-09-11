package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"errors"
	"fmt"
	"github.com/CyberStefNef/skillverk/internal/library"
	"strings"
)

// adoptionOption is one skill that currently lives inside this repository.
type adoptionOption struct {
	name, path, agent string
	tracked           bool
}

func localAdoptions(a *app) []adoptionOption {
	var out []adoptionOption
	for _, sk := range a.data.skills {
		for _, i := range sk.Installations {
			if i.Scope == "project" {
				out = append(out, adoptionOption{sk.Name, i.Path, i.Owner, i.Tracked})
			}
		}
	}
	return out
}

// newAdoptionOfferPage runs once per repository: it explains the choice
// rather than acting on it, and defaults to leaving everything alone.
func newAdoptionOfferPage(a *app) *listPage {
	names := map[string]bool{}
	for _, o := range localAdoptions(a) {
		names[o.name] = true
	}
	head := fmt.Sprintf("%d repository skills found", len(names))
	p := newList(a, head, "Project workflows are often better off staying with their code.", true, false)
	p.setRows([]row{
		{id: "keep", name: "Keep them in this repository", desc: "Nothing moves. You can share them later from the action palette."},
		{id: "share", name: "Choose skills to share…", desc: "Pick which ones move into your shared library."},
	})
	p.back = func(a *app, _ *listPage) tea.Cmd { return acknowledgeAdoption(a) }
	p.enter = func(a *app, p *listPage) tea.Cmd {
		if r, ok := p.focus(); ok && r.id == "share" {
			return openAdoptionReview(a)
		}
		return acknowledgeAdoption(a)
	}
	return p
}

func acknowledgeAdoption(a *app) tea.Cmd {
	if err := a.store.AcknowledgeAdoption(a.repo); err != nil {
		return failure(err)
	}
	a.data.state.AdoptionReviewed = true
	return tea.Sequence(pop, note("Repository skills left in place."))
}

// openAdoptionReview records that the offer was seen, then lists the skills.
func openAdoptionReview(a *app) tea.Cmd {
	if err := a.store.AcknowledgeAdoption(a.repo); err != nil {
		return failure(err)
	}
	a.data.state.AdoptionReviewed = true
	return swap(newAdoptionReviewPage(a))
}

func newAdoptionReviewPage(a *app) *listPage {
	options := localAdoptions(a)
	chosen := map[string]bool{}
	p := newList(a, "Share repository skills", "Unselected skills stay exactly where they are.", true, true)
	p.keys = []key.Binding{keys.All}
	fill := func() {
		var rows []row
		for _, o := range options {
			check := checkOff
			if chosen[o.path] {
				check = checkOn
			}
			badge := o.agent
			if o.tracked {
				badge += " · Git-tracked"
			}
			rows = append(rows, row{id: o.path, name: o.name, desc: library.Clean(o.path), badge: badge, check: check, data: o})
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
		if k.String() != "a" {
			return nil
		}
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
	}
	p.notes = func(a *app, _ *listPage) []string {
		return []string{a.theme.Muted.Render(fmt.Sprintf("%d selected of %d", countChosen(chosen), len(options)))}
	}
	p.enter = func(a *app, _ *listPage) tea.Cmd {
		paths := chosenNames(chosen)
		if len(paths) == 0 {
			return tea.Sequence(toRoot, note("All skills kept in this repository."))
		}
		return push(newConfirmPage("Share repository skills", "Move and remove originals", migrationEffects(paths), func(a *app) tea.Cmd {
			a.jobDone = migrationReport(a, paths)
			return a.perform("Migrating", func() ([]library.Result, error) {
				return a.store.Migrate(a.repo, paths, false, true)
			})
		}))
	}
	return p
}

// migrationEffects states, in full, what confirming a migration will do.
func migrationEffects(paths []string) string {
	return strings.Join([]string{
		"Move these into the shared library and remove the originals:",
		"",
		library.Clean(strings.Join(paths, "\n")),
		"",
		"Confirming will:",
		"  · copy each skill into the shared library, reusing identical copies",
		"  · replace the local originals with managed links",
		"  · delete the saved originals once migration succeeds",
		"  · remove Git-tracked originals from tracking and stage their deletion",
		"  · remove any selected global originals for every repository",
		"",
		"Cancelling leaves the originals and Git tracking untouched and creates no library copies.",
	}, "\n")
}

// migrationReport keeps a partly-failed migration reviewable instead of
// collapsing it into a one-line failure.
func migrationReport(a *app, paths []string) func(*app, jobMsg) tea.Cmd {
	return func(a *app, v jobMsg) tea.Cmd {
		if v.err == nil {
			return nil
		}
		var conflict *library.ConflictError
		if errors.As(v.err, &conflict) {
			body := library.Clean(conflict.Error()) + "\n\nReplacing changes shared content in every repository that uses these skills."
			return tea.Sequence(func() tea.Msg { return reloadMsg{} }, push(newConfirmPage("Replace existing skills", "Replace", body, func(a *app) tea.Cmd {
				return a.perform("Migrating", func() ([]library.Result, error) {
					return a.store.Migrate(a.repo, paths, true, true)
				})
			})))
		}
		if err := a.refresh(); err != nil {
			return failure(err)
		}
		body := strings.Join([]string{
			"Migration incomplete.",
			"",
			resultLines(v.results, v.err),
			"",
			"Successful imports were kept. Originals involved in failures were preserved.",
			"Esc returns to the skill list, where Remove from library undoes an import.",
			"Saved originals can only be removed after a successful migration.",
		}, "\n")
		return tea.Sequence(toRoot, func() tea.Msg { return reloadMsg{} }, push(newDocPage("Migration incomplete", "", body)))
	}
}
