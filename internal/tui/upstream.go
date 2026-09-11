package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/CyberStefNef/skillverk/internal/library"
	"strings"
)

// Recovering a lost upstream closes the gap adoption leaves behind.
//
// Adopting a skill copies what is on this machine. It learns where that copy
// sat, never where it was published, so an adopted skill has nothing to check
// itself against and nothing to update from. This searches the sources the
// library already follows for a skill of the same name, says plainly what each
// match does and does not prove, and records the link when one is chosen.
//
// The search runs and answers inside the pane. Starting it from a row and being
// sent to another screen to read the result loses the place you were in, and a
// list of three candidates never needed a screen of its own.
//
// Matching by name is not proof, and the rows never pretend otherwise. An
// identical copy settles it; anything less is labelled as the guess it is.

// upstreamMsg carries the result of a search back to the pane that started it.
type upstreamMsg struct {
	id         uint64
	name       string
	candidates []library.Candidate
	err        error
}

// findUpstream searches every source the library follows. It is cancellable:
// each source is a clone, and several of them is a long time to hold someone up.
func findUpstream(a *app, sk library.Skill) tea.Cmd {
	return a.load("Searching your sources for "+sk.Name, func(ctx context.Context, id uint64) tea.Msg {
		found, err := a.store.FindUpstream(ctx, sk.Name)
		return upstreamMsg{id: id, name: sk.Name, candidates: found, err: err}
	})
}

// found records a finished search on the page, so the candidates appear under
// the action that asked for them.
func (p *skillsPage) found(a *app, v upstreamMsg) tea.Cmd {
	if v.id != a.jobID {
		return nil
	}
	a.stop()
	p.working = -1
	if v.err != nil {
		return failure(v.err)
	}
	if len(v.candidates) == 0 {
		// The search coming back empty is the moment someone asks why this
		// skill has no source at all, so the answer goes here rather than on a
		// screen they would have to know to look for.
		detail := "Skillverk searched every source your library already follows and found\nnothing of that name."
		if sk := p.skill(); sk != nil && sk.Entry != nil {
			if short, why := sourceProblem(*sk.Entry); short != "" {
				detail = sk.Name + " " + short + ".\n\n" + why + "\n\n" + detail
			}
		}
		return func() tea.Msg {
			return noteMsg{text: "No source you follow holds a skill called " + v.name + ".", detail: detail, lvl: levelWarn}
		}
	}
	p.upstream, p.upstreamFor = v.candidates, v.name
	// Land the cursor on the first candidate: it is what the search was for,
	// and the best match is first.
	p.action = len(p.agents(a)) + len(p.actions(a))
	return note("Enter records where %s is published. Nothing is downloaded.", v.name)
}

// compared reports a finished check. Up to date is the whole answer and needs
// no further screen; anything else is the moment to decide whether to replace,
// so the verdict is the confirmation rather than a note you must act on
// separately.
func (p *skillsPage) compared(a *app, v compareMsg) tea.Cmd {
	if v.id != a.jobID {
		return nil
	}
	a.stop()
	p.working = -1
	if v.err != nil {
		return failure(v.err)
	}
	if v.drift.Same {
		return done("%s is up to date.", v.name)
	}
	head, body := driftReport(v.name, v.drift)
	return push(newConfirmPage(v.name, "Replace my copy", strings.Join([]string{
		head, "", body, "",
		"Replacing fetches the source's current version and puts it everywhere this",
		"skill is turned on.",
	}, "\n"), func(a *app) tea.Cmd {
		return tea.Sequence(toRoot, a.perform("Replacing "+v.name, func() ([]library.Result, error) {
			return a.store.Refresh([]string{v.name})
		}))
	}))
}

// chooseUpstream records a candidate as where the skill is published, which is
// what makes checking and updating work for a skill that was adopted.
func (p *skillsPage) chooseUpstream(a *app, c library.Candidate) tea.Cmd {
	name := p.upstreamFor
	if err := a.store.SetUpstream(name, c.Upstream, c.UpstreamPath); err != nil {
		return failure(err)
	}
	p.upstream, p.upstreamFor = nil, ""
	return tea.Sequence(reload, done("%s now follows %s.", name, repoURL(c.Upstream)))
}

// proves says what a match establishes, which is the only thing that makes
// these rows safe to act on.
func proves(c library.Candidate) (string, string, level) {
	switch {
	case c.Identical:
		return "Byte-for-byte the same as your copy. This is where it came from.",
			"certain", levelDone
	case c.SameDescription:
		return "Same name and description, contents since diverged. Very likely the same skill.",
			"likely", levelInfo
	}
	return "Same name only. This may be a different skill that shares the name.",
		"unconfirmed", levelWarn
}
