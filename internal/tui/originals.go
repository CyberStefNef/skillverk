package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/CyberStefNef/skillverk/internal/library"
	"path/filepath"
	"strings"
)

// newOriginalsPage reviews originals kept after a migration. Each one is
// removed individually, after its full paths are shown.
func newOriginalsPage(a *app, name string) *listPage {
	lead := "Originals kept after a successful migration. Removing one is permanent."
	p := newList(a, "Saved originals", lead, true, false)
	fill := func(a *app) {
		var rows []row
		for _, o := range a.data.originals {
			if name != "" && o.Name != name {
				continue
			}
			rows = append(rows, row{id: o.ID, name: o.Name, desc: library.Clean(o.Path), badge: o.Scope, data: o})
		}
		p.setRows(rows)
	}
	fill(a)
	p.refreshed = func(a *app, _ *listPage) tea.Cmd { fill(a); return nil }
	p.notes = func(a *app, p *listPage) []string {
		r, ok := p.focus()
		if !ok {
			return []string{a.theme.Muted.Render("Nothing retained. Migrations either kept originals in place or finished cleanly.")}
		}
		o := r.data.(library.Original)
		return []string{a.theme.Muted.Render("Retained at " + library.Clean(o.Backup))}
	}
	p.enter = func(a *app, p *listPage) tea.Cmd {
		r, ok := p.focus()
		if !ok {
			return nil
		}
		o := r.data.(library.Original)
		body := strings.Join([]string{
			"Permanently remove the original of " + o.Name + ".",
			"",
			"Original path: " + library.Clean(o.Path),
			"Retained copy: " + library.Clean(o.Backup),
			globalWarning(o.Scope),
		}, "\n")
		return push(newConfirmPage("Remove saved original", "Remove", body, func(a *app) tea.Cmd {
			return a.perform("Removing original", func() ([]library.Result, error) {
				result, err := a.store.CleanupOriginal(o.ID, true)
				return []library.Result{result}, err
			})
		}))
	}
	return p
}

// newAdoptPage moves one existing installation into the shared library.
func newAdoptPage(a *app, sk library.Skill, installs []library.Installation) *listPage {
	lead := "Choose which installation of " + sk.Name + " becomes the shared copy."
	p := newList(a, "Move to shared library", lead, true, false)
	var rows []row
	for _, i := range installs {
		rows = append(rows, row{id: i.Path, name: filepath.Base(i.Path), desc: library.Clean(i.Path),
			badge: i.Scope + " · " + i.Owner, data: i})
	}
	p.setRows(rows)
	p.notes = func(a *app, _ *listPage) []string {
		return []string{a.theme.Muted.Render("The next screen lists the exact effects and asks for confirmation.")}
	}
	p.enter = func(a *app, p *listPage) tea.Cmd {
		r, ok := p.focus()
		if !ok {
			return nil
		}
		return push(adoptConfirm(a, sk, r.data.(library.Installation)))
	}
	return p
}

// adoptConfirm states what taking over one installation will do, and does it.
func adoptConfirm(a *app, sk library.Skill, i library.Installation) *confirmPage {
	return newConfirmPage("Move to shared library", "Move", migrationEffects([]string{i.Path}), func(a *app) tea.Cmd {
		a.jobDone = migrationReport(a, []string{i.Path})
		return a.perform("Migrating "+sk.Name, func() ([]library.Result, error) {
			return a.store.Migrate(a.repo, []string{i.Path}, false, true)
		})
	})
}

func globalWarning(scope string) string {
	if scope == "global" {
		return "\nThis original is global: removing it affects repositories Skillverk has never seen."
	}
	return ""
}
