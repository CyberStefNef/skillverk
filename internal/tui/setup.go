package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/CyberStefNef/skillverk/internal/library"
	"github.com/CyberStefNef/skillverk/internal/onboarding"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// scanMsg carries a finished setup scan back to the offer screen.
type scanMsg struct {
	id   uint64
	plan library.SetupPlan
	err  error
}

// setupOfferPage is the first thing a new user sees. It says what will happen
// and defaults to looking, not moving.
type setupOfferPage struct {
	*listPage
	roots []string
}

func newSetupOfferPage(roots []string) *setupOfferPage {
	return &setupOfferPage{roots: roots}
}

// build defers construction until the app exists, so Run can push this page
// before the first layout pass.
func (p *setupOfferPage) build(a *app) {
	if p.listPage != nil {
		return
	}
	p.listPage = newList(a, "Set up Skillverk", "Find the skills already installed on this machine and organise them.", true, false)
	p.setRows([]row{
		{id: "review", name: "Review existing skills", desc: "Look at what is installed. Nothing moves without your approval."},
		{id: "skip", name: "Skip for now", desc: "Go straight to the skill list. Available later from the action palette."},
	})
	p.notes = func(a *app, _ *listPage) []string {
		return []string{a.theme.Muted.Render("Review runs " + reviewSummary(a) + ". Press s to change.")}
	}
	p.keys = []key.Binding{bind("s", "review settings")}
	p.enter = func(a *app, l *listPage) tea.Cmd {
		if r, ok := l.focus(); ok && r.id == "skip" {
			if err := a.store.MarkSetupReviewed(); err != nil {
				return failure(err)
			}
			return tea.Sequence(toRoot, note("Setup skipped. Reopen it from the action palette."))
		}
		return p.begin(a)
	}
	p.other = func(a *app, _ *listPage, k tea.KeyPressMsg) tea.Cmd {
		if k.String() == "s" {
			return push(newSettingsPage(a))
		}
		return nil
	}
}

func (p *setupOfferPage) title() string { return "Set up Skillverk" }
func (p *setupOfferPage) lead() string {
	return "Find the skills already installed on this machine and organise them."
}
func (p *setupOfferPage) typing() bool { return false }
func (p *setupOfferPage) layout(a *app, width, height int) {
	p.build(a)
	p.listPage.layout(a, width, height)
}
func (p *setupOfferPage) view(a *app) string { p.build(a); return p.listPage.view(a) }
func (p *setupOfferPage) bindings() []key.Binding {
	if p.listPage == nil {
		return []key.Binding{keys.Up, keys.Down, keys.Choose, keys.Back}
	}
	return p.listPage.bindings()
}

func (p *setupOfferPage) update(a *app, msg tea.Msg) tea.Cmd {
	p.build(a)
	if v, ok := msg.(scanMsg); ok {
		if v.id != a.jobID {
			return nil
		}
		a.stop()
		if v.err != nil {
			return failure(v.err)
		}
		return push(newSetupReviewPage(a, v.plan))
	}
	return p.listPage.update(a, msg)
}

// begin either hands the review to an AI provider or scans locally.
func (p *setupOfferPage) begin(a *app) tea.Cmd {
	provider := onboarding.Resolve(a.data.review.Provider)
	if provider == "local" {
		return p.scan(a)
	}
	cmd, err := onboarding.StartReview(provider, a.data.review.Models[provider], a.store, a.repo, p.roots)
	if err != nil {
		return failure(err)
	}
	dir := cmd.Dir
	return tea.Sequence(toRoot, tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return noteMsg{text: "Review ended with an error.", detail: library.Clean(err.Error()), lvl: levelError}
		}
		return noteMsg{text: "Review ended. Files saved in " + library.Clean(dir), lvl: levelDone}
	}))
}

func (p *setupOfferPage) scan(a *app) tea.Cmd {
	roots := p.roots
	if len(roots) == 0 {
		roots = library.DefaultSetupRoots()
	}
	return a.load("Scanning for installed skills", func(ctx context.Context, id uint64) tea.Msg {
		plan, err := a.store.ScanSetup(ctx, roots)
		return scanMsg{id: id, plan: plan, err: err}
	})
}

// setupActions cycles what happens to one scanned installation.
var setupActions = map[string]string{
	"keep":           "keep in place",
	"share":          "move to library",
	"replace-shared": "replace library copy",
	"remove-broken":  "remove broken link",
}

// newSetupReviewPage decides each scanned item individually. Everything
// defaults to whatever the scan proposed, and "keep" is always one key away.
func newSetupReviewPage(a *app, plan library.SetupPlan) *listPage {
	lead := "Space chooses move or keep. Repository skills stay put unless you pick them."
	p := newList(a, "Review setup", lead, true, true)
	p.keys = []key.Binding{bind("x", "replace library copy"), bind("v", "details")}
	fill := func() {
		var rows []row
		for i, item := range plan.Items {
			badge, tone := setupActions[item.Action], levelInfo
			if item.Action != "keep" {
				tone = levelDone
			}
			if item.Conflict != "" {
				badge += " · conflict"
				tone = levelWarn
			}
			rows = append(rows, row{id: item.Path, name: item.Name, desc: library.Clean(item.Reason),
				badge: badge + " · " + item.Scope, tone: tone, data: i})
		}
		p.setRows(rows)
	}
	fill()
	current := func(p *listPage) (*library.SetupItem, bool) {
		r, ok := p.focus()
		if !ok {
			return nil, false
		}
		return &plan.Items[r.data.(int)], true
	}
	p.toggle = func(a *app, p *listPage) tea.Cmd {
		item, ok := current(p)
		if !ok {
			return nil
		}
		if item.Action == "keep" {
			item.Action = "share"
			if strings.HasPrefix(item.Reason, "Broken link") {
				item.Action = "remove-broken"
			}
		} else {
			item.Action = "keep"
		}
		fill()
		return nil
	}
	p.other = func(a *app, p *listPage, k tea.KeyPressMsg) tea.Cmd {
		item, ok := current(p)
		if !ok {
			return nil
		}
		switch k.String() {
		case "x":
			if item.Action == "share" {
				item.Action = "replace-shared"
				fill()
			}
			return nil
		case "v":
			return push(newDocPage(item.Name, library.Clean(item.Path), setupDetails(*item)))
		}
		return nil
	}
	p.notes = func(a *app, p *listPage) []string {
		moving := 0
		for _, item := range plan.Items {
			if item.Action != "keep" {
				moving++
			}
		}
		lines := []string{a.theme.Muted.Render(fmt.Sprintf("%d of %d will change", moving, len(plan.Items)))}
		if item, ok := current(p); ok {
			lines = append(lines, a.theme.Muted.Render(library.Clean(item.Path)))
		}
		if n := len(plan.Warnings); n > 0 {
			lines = append(lines, a.theme.Warn.Render(fmt.Sprintf("! %d scan warnings, shown before you approve anything", n)))
		}
		return lines
	}
	p.enter = func(a *app, _ *listPage) tea.Cmd {
		snapshot := plan
		body := library.Clean(snapshot.Summary())
		if len(snapshot.Warnings) > 0 {
			body += "\n\nScan warnings\n" + library.Clean(strings.Join(snapshot.Warnings, "\n"))
		}
		return push(newConfirmPage("Apply setup", "Apply", body, func(a *app) tea.Cmd {
			return a.perform("Applying setup", func() ([]library.Result, error) {
				results, err := a.store.ApplySetup(context.Background(), snapshot, true)
				if err == nil {
					err = a.store.MarkSetupReviewed()
				}
				return results, err
			})
		}))
	}
	return p
}

// setupDetails shows everything known about one scanned installation,
// including the instructions the skill would contribute.
func setupDetails(item library.SetupItem) string {
	parts := []string{library.Clean(item.Path), "", library.Clean(item.Reason)}
	if item.Conflict != "" {
		parts = append(parts, "", "Conflict: "+library.Clean(item.Conflict))
	}
	if names := siblingNames(item.Siblings); len(names) > 0 {
		parts = append(parts, "", "Uses shared skills: "+strings.Join(names, ", "))
	}
	if preview := item.ManagerPreview(); preview != "" {
		parts = append(parts, "", library.Clean(preview))
	}
	if item.Tracked {
		parts = append(parts, "", "Tracked by Git. Moving this skill stages its removal.")
	}
	if body, short := readSkillBody(item.Path); body != "" {
		parts = append(parts, "", "Skill instructions", "", body)
		if short {
			parts = append(parts, "", "Preview shortened. Open SKILL.md to read the rest.")
		}
	}
	return strings.Join(parts, "\n")
}

func siblingNames(refs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, ref := range refs {
		name := strings.Split(strings.TrimPrefix(ref, "../"), "/")[0]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

const previewLimit = 64 * 1024

func readSkillBody(dir string) (string, bool) {
	f, err := os.Open(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return "", false
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, previewLimit+1))
	if err != nil {
		return "", false
	}
	return string(body[:min(len(body), previewLimit)]), len(body) > previewLimit
}
