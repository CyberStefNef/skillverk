package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/CyberStefNef/skillverk/internal/library"
	"net/url"
	"strings"
)

// action is one entry in the palette. Everything the interface can do is
// reachable from here, by name, with live search. Nothing depends on the
// user remembering a letter.
type action struct {
	name string
	desc string
	run  func(a *app) tea.Cmd
	// danger marks an action that destroys something. Such a row says so in
	// the warning colour and names the act plainly: a list where deleting
	// content out of every repository reads the same as turning a link on
	// leaves the weight of the choice to be discovered on the next screen.
	danger bool
}

// newActionsPage lists what can be done to one skill. Anything that is not
// about this skill lives in the menu, so neither list is long enough to need
// reading twice.
func newActionsPage(a *app, sk *library.Skill) page {
	if sk == nil {
		return newMenuPage(a)
	}
	return newPalette(a, "Actions · "+sk.Name, func(a *app) []action { return skillActions(a, *sk) })
}

func newPalette(a *app, title string, build func(*app) []action) *listPage {
	p := newList(a, title, "", false, true)
	p.detail = func(a *app, p *listPage, width int) []string {
		r, ok := p.focus()
		if !ok {
			return nil
		}
		return append([]string{a.theme.Heading.Render(r.name), ""}, wrap(a.theme.Muted, r.desc, width, 6)...)
	}
	p.enter = func(a *app, p *listPage) tea.Cmd {
		r, ok := p.focus()
		if !ok {
			return nil
		}
		return r.data.(action).run(a)
	}
	p.refreshed = func(a *app, p *listPage) tea.Cmd { p.setRows(actionRows(build(a))); return nil }
	p.setRows(actionRows(build(a)))
	return p
}

func actionRows(actions []action) []row {
	var out []row
	for _, act := range actions {
		r := row{id: act.name, name: act.name, desc: act.desc, data: act}
		if act.danger {
			r.badge, r.tone = "deletes", levelWarn
		}
		out = append(out, r)
	}
	return out
}

// skillActions are the operations that apply to one skill.
func skillActions(a *app, sk library.Skill) []action {
	var out []action
	add := func(name, desc string, run func(*app) tea.Cmd) {
		out = append(out, action{name: name, desc: desc, run: run})
	}
	destroys := func(name, desc string, run func(*app) tea.Cmd) {
		out = append(out, action{name: name, desc: desc, run: run, danger: true})
	}
	if a.repo != "" && (sk.Entry != nil || sk.Selected) && !sk.HasScope("project") {
		name, on := "Turn on here", true
		desc := "Link " + sk.Name + " into this repository for " + a.agentLabel()
		if sk.Selected {
			name, on, desc = "Turn off here", false, "Unlink "+sk.Name+" from this repository. The library copy stays."
		}
		add(name, desc, func(a *app) tea.Cmd {
			return tea.Sequence(toRoot, a.perform(name, func() ([]library.Result, error) {
				return a.store.Select(a.repo, []string{sk.Name}, on)
			}))
		})
	}
	if sk.Entry != nil {
		target := origin(*sk.Entry)
		if u, err := url.Parse(target); err == nil && u.Host == "github.com" && (u.Scheme == "https" || u.Scheme == "http") {
			add("Open source in browser", "Open this skill's GitHub folder", func(*app) tea.Cmd {
				return openSource(target)
			})
		}
		if short, _ := sourceProblem(*sk.Entry); short != "" {
			add("Find its source…", "Search the sources you already follow for where "+sk.Name+" came from",
				func(a *app) tea.Cmd { return findUpstream(a, sk) })
		} else {
			add("Update from its source…", "Compare against the source and replace your copy if you want to. Checks before it changes anything.",
				func(a *app) tea.Cmd {
					return a.load("Checking "+sk.Name, func(ctx context.Context, id uint64) tea.Msg {
						drift, err := a.store.Compare(ctx, sk.Name)
						return compareMsg{id: id, name: sk.Name, drift: drift, err: err}
					})
				})
		}
	}
	if a.repo != "" && sk.Entry != nil && !sk.HasScope("project") {
		add("Copy into this repository…", "Commit a copy to Git so the repository carries it. It stops following updates.",
			func(a *app) tea.Cmd {
				body := strings.Join([]string{
					"Make " + sk.Name + " repository-owned.",
					"",
					"Creates a local copy and stages it in Git for " + a.agentLabel() + ".",
					"This repository stops following shared updates.",
					"The shared library and every other repository keep their copies.",
				}, "\n")
				return push(newConfirmPage("Make repository-owned", "Make local copy", body, func(a *app) tea.Cmd {
					return tea.Sequence(toRoot, a.perform("Localizing "+sk.Name, func() ([]library.Result, error) {
						return a.store.Localize(a.repo, sk.Name, true)
					}))
				}))
			})
	}
	if installs := adoptable(sk); len(installs) > 0 {
		add("Take over this installation…", "Move a copy Skillverk did not install into your library, and manage it from there",
			func(a *app) tea.Cmd { return push(newAdoptPage(a, sk, installs)) })
	}
	if sk.Entry != nil {
		destroys("Delete from library…", "Delete your copy and unlink it from every repository that uses it",
			func(a *app) tea.Cmd {
				body := strings.Join([]string{
					"Delete the shared library copy of " + sk.Name + ".",
					library.Clean(sk.Path),
					"",
					"This also removes its recorded managed links in other repositories.",
					"Existing unmanaged originals and saved originals are kept.",
					"Unreachable paths stay pending until they can be cleaned up.",
				}, "\n")
				return push(newConfirmPage("Delete from library", "Delete", body, func(a *app) tea.Cmd {
					return tea.Sequence(toRoot, a.perform("Deleting "+sk.Name, func() ([]library.Result, error) {
						return a.store.Delete(sk.Name, true)
					}))
				}))
			})
	}
	for _, o := range a.data.originals {
		if o.Name == sk.Name {
			destroys("Delete the saved original…", "Remove the untouched copy kept when "+sk.Name+" was taken over",
				func(a *app) tea.Cmd { return push(newOriginalsPage(a, sk.Name)) })
			break
		}
	}
	return out
}

// adoptable lists installations of a skill that Skillverk could take over.
func adoptable(sk library.Skill) []library.Installation {
	var out []library.Installation
	for _, i := range sk.Installations {
		if i.Scope == "project" || i.Scope == "global" {
			out = append(out, i)
		}
	}
	return out
}
