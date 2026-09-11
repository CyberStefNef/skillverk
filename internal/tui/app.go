// Package tui renders Skillverk's interactive interface.
//
// The interface is a stack of pages over one persistent frame. Every page
// draws inside the same centred column, under the same repository header and
// above the same status and help footer, so navigation has a single rule:
// Enter goes forward, Esc comes back.
package tui

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"context"
	"errors"
	"fmt"
	"github.com/CyberStefNef/skillverk/internal/library"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// page is one screen. Pages mutate themselves and ask the app to navigate by
// returning commands; they never reach into the stack directly.
type page interface {
	title() string
	lead() string
	// typing reports that the page owns every printable key right now, which
	// keeps single-letter shortcuts out of text fields.
	typing() bool
	layout(a *app, width, height int)
	update(a *app, msg tea.Msg) tea.Cmd
	view(a *app) string
	bindings() []key.Binding
}

// Navigation messages. Pages emit these instead of editing the stack.
type (
	pushMsg struct{ p page }
	swapMsg struct{ p page }
	popMsg  struct{ n int }
	rootMsg struct{}
	noteMsg struct {
		text   string
		detail string
		lvl    level
	}
	reloadMsg struct{}
	// jobMsg carries the outcome of work started with app.perform.
	jobMsg struct {
		id      uint64
		results []library.Result
		err     error
	}
	// tickMsg expires a transient status note.
	tickMsg struct{ id uint64 }
	// compareMsg carries the outcome of an update check back to the pane that
	// started it.
	compareMsg struct {
		id    uint64
		name  string
		drift library.Drift
		err   error
	}
)

func push(p page) tea.Cmd { return func() tea.Msg { return pushMsg{p} } }
func swap(p page) tea.Cmd { return func() tea.Msg { return swapMsg{p} } }
func pop() tea.Msg        { return popMsg{1} }
func toRoot() tea.Msg     { return rootMsg{} }
func reload() tea.Msg     { return reloadMsg{} }
func note(f string, v ...any) tea.Cmd {
	return func() tea.Msg { return noteMsg{text: fmt.Sprintf(f, v...), lvl: levelInfo} }
}
func done(f string, v ...any) tea.Cmd {
	return func() tea.Msg { return noteMsg{text: fmt.Sprintf(f, v...), lvl: levelDone} }
}
func warn(f string, v ...any) tea.Cmd {
	return func() tea.Msg { return noteMsg{text: fmt.Sprintf(f, v...), lvl: levelWarn} }
}
func failure(err error) tea.Cmd {
	return func() tea.Msg {
		text := library.Clean(err.Error())
		head, _, multi := strings.Cut(text, "\n")
		if multi || ansi.StringWidth(head) > 72 {
			return noteMsg{text: ansi.Truncate(head, 72, "…"), detail: text, lvl: levelError}
		}
		return noteMsg{text: text, lvl: levelError}
	}
}

// snapshot is everything the pages read about the library, refreshed together
// so no screen shows a mix of old and new state.
type snapshot struct {
	skills    []library.Skill
	state     library.State
	originals []library.Original
	review    library.ReviewSettings
}

type app struct {
	store *library.Store
	repo  string
	data  snapshot

	width, height int
	theme         theme
	help          help.Model
	spinner       spinner.Model

	stack []page

	// Background work. Only one job runs at a time; jobID discards results
	// from a job the user has already cancelled or replaced.
	busy    string
	jobID   uint64
	cancel  context.CancelFunc
	ended   <-chan struct{}
	jobDone func(*app, jobMsg) tea.Cmd

	// Status line under the body.
	status    noteMsg
	statusID  uint64
	quitAfter bool
}

func newApp(s *library.Store, repo string) (*app, error) {
	a := &app{store: s, repo: repo, width: 96, height: 30, theme: newTheme(darkBackground())}
	a.help = help.New()
	a.help.Styles = a.theme.Help
	a.spinner = spinner.New(spinner.WithSpinner(spinner.Dot))
	if err := a.refresh(); err != nil {
		return nil, err
	}
	if results, err := s.Reconcile(repo); err != nil {
		a.status = noteMsg{text: "Pending repository changes need attention.", detail: resultLines(results, err), lvl: levelWarn}
	}
	a.stack = []page{newSkillsPage(a)}
	return a, nil
}

// refresh reloads every store-backed value the pages render.
func (a *app) refresh() error {
	skills, err := a.store.Catalog(a.repo)
	if err != nil {
		return err
	}
	state, err := library.ReadState(a.repo)
	if err != nil {
		return err
	}
	originals, err := a.store.Originals("")
	if err != nil {
		return err
	}
	review, err := a.store.ReviewSettings()
	if err != nil {
		return err
	}
	a.data = snapshot{skills: skills, state: state, originals: originals, review: review}
	return nil
}

func (a *app) top() page { return a.stack[len(a.stack)-1] }

// perform runs a library operation, showing the spinner until it finishes.
func (a *app) perform(label string, fn func() ([]library.Result, error)) tea.Cmd {
	a.busy = label
	a.jobID++
	id := a.jobID
	a.cancel, a.ended = nil, nil
	return tea.Batch(a.spinner.Tick, func() tea.Msg {
		results, err := fn()
		return jobMsg{id: id, results: results, err: err}
	})
}

// load runs cancellable background work. The page owns the result message and
// must ignore any whose id no longer matches app.jobID.
func (a *app) load(label string, fn func(context.Context, uint64) tea.Msg) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	ended := make(chan struct{})
	a.busy, a.cancel, a.ended = label, cancel, ended
	a.jobID++
	id := a.jobID
	return tea.Batch(a.spinner.Tick, func() tea.Msg {
		defer close(ended)
		return fn(ctx, id)
	})
}

// stop abandons cancellable work without waiting for it to unwind.
func (a *app) stop() {
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.busy = ""
	a.jobID++
}

func (a *app) Init() tea.Cmd { return nil }

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = v.Width, v.Height
		return a, nil
	case spinner.TickMsg:
		if a.busy == "" {
			return a, nil
		}
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(v)
		return a, cmd
	case pushMsg:
		a.stack = append(a.stack, v.p)
		return a, a.resize()
	case swapMsg:
		a.stack[len(a.stack)-1] = v.p
		return a, a.resize()
	case popMsg:
		a.unwind(max(1, len(a.stack)-max(1, v.n)))
		return a, a.resize()
	case rootMsg:
		a.unwind(1)
		return a, a.resize()
	case noteMsg:
		a.status = v
		a.statusID++
		if v.lvl == levelInfo || v.lvl == levelDone {
			id := a.statusID
			return a, tea.Tick(6*time.Second, func(time.Time) tea.Msg { return tickMsg{id} })
		}
		return a, nil
	case tickMsg:
		if v.id == a.statusID {
			a.status = noteMsg{}
		}
		return a, nil
	case reloadMsg:
		if err := a.refresh(); err != nil {
			return a, failure(err)
		}
		return a, a.top().update(a, reloadMsg{})
	case jobMsg:
		return a, a.finish(v)
	case tea.KeyPressMsg:
		return a, a.press(v)
	}
	return a, a.top().update(a, msg)
}

// finish reports a completed operation. A page may claim the result first,
// which is how an import turns a name conflict into a replacement question.
func (a *app) finish(v jobMsg) tea.Cmd {
	if v.id != a.jobID {
		return nil
	}
	// The job is over, so release its context even though nothing cancelled it.
	if a.cancel != nil {
		a.cancel()
	}
	a.busy, a.cancel, a.ended = "", nil, nil
	hook := a.jobDone
	a.jobDone = nil
	if hook != nil {
		if cmd := hook(a, v); cmd != nil {
			return cmd
		}
	}
	if err := a.refresh(); err != nil {
		return failure(err)
	}
	detail := resultLines(v.results, v.err)
	cmds := []tea.Cmd{toRoot, func() tea.Msg { return reloadMsg{} }}
	switch {
	case v.err != nil:
		cmds = append(cmds, func() tea.Msg {
			return noteMsg{text: "Some changes need attention. Press ! for details.", detail: detail, lvl: levelError}
		})
	case detail != "":
		cmds = append(cmds, func() tea.Msg {
			return noteMsg{text: summarize(v.results), detail: detail, lvl: levelDone}
		})
	}
	return tea.Sequence(cmds...)
}

// performHere runs a change without unwinding to the skill list, for a pane
// that owns the row the change was started from: the menu's harness switches
// and saved originals are a list of changes to make, and being thrown back to
// the skills screen after each one loses the place they were made from.
func (a *app) performHere(label string, fn func() ([]library.Result, error)) tea.Cmd {
	a.jobDone = func(a *app, v jobMsg) tea.Cmd {
		if err := a.refresh(); err != nil {
			return failure(err)
		}
		detail := resultLines(v.results, v.err)
		switch {
		case v.err != nil:
			return tea.Sequence(reload, func() tea.Msg {
				return noteMsg{text: "Some changes need attention. Press ! for details.", detail: detail, lvl: levelError}
			})
		case detail != "":
			return tea.Sequence(reload, func() tea.Msg {
				return noteMsg{text: summarize(v.results), detail: detail, lvl: levelDone}
			})
		}
		return reload
	}
	return a.perform(label, fn)
}

// driftReport turns a verdict into something worth reading. The outcomes want
// different next steps. One is safe, one would throw away your own work, which
// is the entire reason the baseline is recorded.
func driftReport(name string, d library.Drift) (string, string) {
	switch d.Verdict() {
	case "out of date":
		return name + " is out of date.", strings.Join([]string{
			"Your copy is exactly as it arrived. The source has been updated since.",
			"",
			"Replacing it loses nothing of yours, because nothing here was changed.",
		}, "\n")
	case "edited here":
		return name + " was edited here.", strings.Join([]string{
			"The source is exactly as it was when you took this copy. The changes are",
			"yours, and there is no update to take.",
			"",
			"Replacing it would discard your edits.",
		}, "\n")
	case "diverged":
		return name + " has diverged from its source.", strings.Join([]string{
			"Both sides changed since you took this copy: the source was updated and",
			"this copy was edited here.",
			"",
			"Replacing it would lose your edits. Copy anything you want to keep out",
			"of it first. skillverk path " + name + " prints where it is.",
		}, "\n")
	}
	return name + " differs from its source.", strings.Join([]string{
		name + " and its source are not the same content.",
		"",
		"Which of them moved is not recorded: this copy predates Skillverk keeping",
		"track of content as it arrives. Replacing it starts that record, and future",
		"checks will be able to tell you which side moved.",
	}, "\n")
}

// press handles the bindings that work on every screen, then defers to the
// page. Cancelling a running job is the only key accepted while busy.
func (a *app) press(v tea.KeyPressMsg) tea.Cmd {
	k := v.String()
	if k == "ctrl+c" {
		return a.quit()
	}
	if a.busy != "" {
		if a.cancel != nil && (k == "esc" || k == "q") {
			ended := a.ended
			a.stop()
			if k == "q" {
				a.quitAfter = true
				return tea.Sequence(warn("Cancelled."), settle(ended))
			}
			return warn("Cancelled.")
		}
		return nil
	}
	if a.top().typing() {
		return a.top().update(a, v)
	}
	switch {
	case k == "pgup" || k == "pgdown" || k == "home" || k == "end":
		return nil
	case k == "q":
		return a.quit()
	case key.Matches(v, keys.Help):
		a.help.ShowAll = !a.help.ShowAll
		return a.resize()
	case k == "!" && a.status.detail != "":
		return push(newDocPage("Last result", "", a.status.detail))
	}
	return a.top().update(a, v)
}

func (a *app) quit() tea.Cmd {
	ended := a.ended
	a.stop()
	a.quitAfter = true
	return settle(ended)
}

// settle lets cancelled Git subprocesses reap before the program exits.
func settle(ended <-chan struct{}) tea.Cmd {
	if ended == nil {
		return tea.Quit
	}
	return func() tea.Msg {
		select {
		case <-ended:
		case <-time.After(2 * time.Second):
		}
		return tea.Quit()
	}
}

// Wide terminals get more room than prose would want: the skills screen puts
// a list and a pane of paths side by side, and paths do not wrap.
func (a *app) contentWidth() int { return max(24, min(120, a.width-4)) }

// resize recomputes the body box and hands it to the visible page.
func (a *app) resize() tea.Cmd {
	width := a.contentWidth()
	head, foot := a.headerLines(width), a.footerLines(width)
	body := max(3, a.height-lipgloss.Height(head)-lipgloss.Height(foot))
	a.top().layout(a, width, body)
	return nil
}

func (a *app) agentLabel() string {
	var names []string
	for _, agent := range a.data.state.Agents {
		names = append(names, library.AgentLabel(agent))
	}
	if len(names) == 0 {
		return "No harnesses enabled"
	}
	return strings.Join(names, " · ")
}

func (a *app) headerLines(width int) string {
	t := a.theme
	// The right of the bar is for what is wrong, not for what is normal: the
	// harnesses a repository uses are named per skill in the detail pane.
	context, right := "Library only", "Open a Git repository to activate skills"
	if a.repo != "" {
		context, right = filepath.Base(a.repo), ""
		if len(a.data.state.Agents) == 0 {
			right = "No harnesses enabled; press m to choose"
		}
	}
	left := t.Brand.Render("Skillverk") + t.Muted.Render("  "+context)
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	bar := left + strings.Repeat(" ", max(1, gap)) + t.Muted.Render(right)
	if right == "" {
		bar = left
	}
	if gap < 1 {
		bar = ansi.Truncate(left, width, "…")
	}
	var crumbs []string
	for _, p := range a.stack {
		crumbs = append(crumbs, p.title())
	}
	trail := ""
	if solo, ok := a.top().(interface{ soloTitle() bool }); len(crumbs) > 1 && !(ok && solo.soloTitle()) {
		trail = t.Muted.Render(strings.Join(crumbs[:len(crumbs)-1], " › ") + " › ")
	}
	heading := trail + t.Heading.Render(crumbs[len(crumbs)-1])
	lines := []string{"", bar, t.Rule.Render(strings.Repeat("─", width)), "", ansi.Truncate(heading, width, "…")}
	if lead := a.top().lead(); lead != "" {
		lines = append(lines, t.Lead.Render(ansi.Truncate(lead, width, "…")))
	}
	return strings.Join(append(lines, ""), "\n")
}

func (a *app) footerLines(width int) string {
	t := a.theme
	bindings := a.top().bindings()
	if a.status.detail != "" {
		bindings = append(bindings, bind("!", "details"))
	}
	escapes := []key.Binding{keys.Help, keys.Quit}
	if a.help.ShowAll {
		a.help.SetWidth(width)
		view := a.help.FullHelpView(columns(append(bindings, escapes...), 3))
		return strings.Join([]string{"", t.Rule.Render(strings.Repeat("─", width)), a.statusLine(width), view}, "\n")
	}
	// Help and quit are the way out of anything, so they keep their place on
	// the right while the screen's own controls absorb the truncation.
	a.help.SetWidth(0)
	right := a.help.ShortHelpView(escapes)
	budget := max(1, width-ansi.StringWidth(right)-3)
	a.help.SetWidth(budget)
	left := ansi.Truncate(a.help.ShortHelpView(bindings), budget, "…")
	view := left + strings.Repeat(" ", max(1, width-ansi.StringWidth(left)-ansi.StringWidth(right))) + right
	return strings.Join([]string{"", t.Rule.Render(strings.Repeat("─", width)), a.statusLine(width), view}, "\n")
}

// statusLine reports progress or the outcome of the last change.
func (a *app) statusLine(width int) string {
	t := a.theme
	switch {
	case a.busy != "":
		// A screen that shows the work where it was started says it better than
		// a line at the foot of the terminal can, and saying it twice is worse
		// than either.
		if local, ok := a.top().(interface{ showsProgress() bool }); ok && local.showsProgress() {
			return ""
		}
		return ansi.Truncate(t.Brand.Render(a.spinner.View()+a.busy)+t.Muted.Render("   esc cancel"), width, "…")
	case a.status.text != "":
		style, mark := t.status(a.status.lvl)
		return style.Render(ansi.Truncate(mark+library.Clean(a.status.text), width, "…"))
	}
	return ""
}

func (a *app) View() tea.View {
	width := a.contentWidth()
	head, foot := a.headerLines(width), a.footerLines(width)
	height := max(3, a.height-lipgloss.Height(head)-lipgloss.Height(foot))
	a.top().layout(a, width, height)
	body := a.top().view(a)
	if n := lipgloss.Height(body); n < height {
		body += strings.Repeat("\n", height-n)
	}
	indent := strings.Repeat(" ", max(0, (a.width-width)/2))
	var out []string
	for _, line := range strings.Split(head+"\n"+body+"\n"+foot, "\n") {
		out = append(out, ansi.Truncate(indent+line, a.width, ""))
	}
	// On a terminal too short for the frame, drop body lines rather than
	// pushing the footer controls off the screen.
	if extra := len(out) - a.height; extra > 0 && a.height > 0 {
		keep := lipgloss.Height(foot)
		out = append(out[:max(0, len(out)-extra-keep)], out[len(out)-keep:]...)
	}
	v := tea.NewView(strings.Join(out, "\n"))
	v.AltScreen = true
	return v
}

// resultLines renders operation results and any error as reviewable text.
func resultLines(results []library.Result, err error) string {
	var lines []string
	for _, r := range results {
		line := strings.TrimSpace(strings.Join([]string{r.Action, r.Name, r.Agent, library.Clean(r.Path)}, " "))
		if r.Error != "" {
			line += "\n    " + library.Clean(r.Error)
		}
		lines = append(lines, line)
	}
	if err != nil {
		lines = append(lines, "", library.Clean(err.Error()))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// verbs turn the library's result actions into something worth reading in a
// status line.
var verbs = map[string]string{
	"active":                  "turned on",
	"inactive":                "turned off",
	"removed":                 "removed",
	"imported":                "imported",
	"refresh":                 "refreshed",
	"keep":                    "kept",
	"retry":                   "retried",
	"central content deleted": "deleted",
	"pending cleanup":         "left pending",
}

// harnessTally collects the skills that were linked and unlinked in the same
// pass. That pair is one harness change, not two opposite ones, and reporting
// it as "turned on X; turned off X" says nothing true.
type harnessTally struct {
	order  []string
	labels map[string][]string
}

func harnessOnly(results []library.Result) harnessTally {
	on, off := map[string][]string{}, map[string]bool{}
	var order []string
	for _, r := range results {
		if r.Name == "" || r.Error != "" || r.Agent == "" {
			continue
		}
		switch r.Action {
		case "active":
			if len(on[r.Name]) == 0 {
				order = append(order, r.Name)
			}
			on[r.Name] = append(on[r.Name], library.AgentLabel(r.Agent))
		case "inactive", "removed":
			off[r.Name] = true
		}
	}
	out := harnessTally{labels: map[string][]string{}}
	for _, name := range order {
		if off[name] {
			out.order = append(out.order, name)
			out.labels[name] = on[name]
		}
	}
	return out
}

// summarize says what changed, by name where the list is short enough to be
// more useful than a count.
func summarize(results []library.Result) string {
	grouped := map[string][]string{}
	var order []string
	harnessChanges := harnessOnly(results)
	var parts []string
	for _, name := range harnessChanges.order {
		parts = append(parts, name+" now links into "+strings.Join(harnessChanges.labels[name], ", "))
	}
	for _, r := range results {
		if r.Name == "" || r.Error != "" || harnessChanges.labels[r.Name] != nil {
			continue
		}
		verb, known := verbs[r.Action]
		if !known {
			verb = r.Action
		}
		if _, seen := grouped[verb]; !seen {
			order = append(order, verb)
		}
		if !slices.Contains(grouped[verb], r.Name) {
			grouped[verb] = append(grouped[verb], r.Name)
		}
	}
	for _, verb := range order {
		names := grouped[verb]
		if len(names) > 3 {
			parts = append(parts, fmt.Sprintf("%s %d skills", verb, len(names)))
			continue
		}
		parts = append(parts, verb+" "+strings.Join(names, ", "))
	}
	if len(parts) == 0 {
		return "Done."
	}
	// No nudge after an import: inside a repository the import asks whether to
	// turn the skills on, and outside one there is nothing to turn them on for.
	text := strings.Join(parts, "; ")
	return strings.ToUpper(text[:1]) + text[1:] + "."
}

func interactive() error {
	if !isTerminal() {
		return errors.New("this screen needs an interactive terminal; try skillverk list or --help")
	}
	return nil
}

func run(a *app, err error) error {
	if err != nil {
		return err
	}
	final, err := tea.NewProgram(a).Run()
	if x, ok := final.(*app); ok {
		x.stop()
		x.unwind(0)
	}
	return err
}

// unwind trims the stack to depth, releasing anything the discarded pages
// hold open, such as an import source keeping a temporary checkout alive.
func (a *app) unwind(depth int) {
	for _, p := range a.stack[max(1, depth):] {
		if c, ok := p.(interface{ close() }); ok {
			c.close()
		}
	}
	a.stack = a.stack[:max(1, depth)]
}

// darkBackground asks the terminal once, before the program starts, so every
// style is resolved before the first frame.
func darkBackground() bool {
	if !isTerminal() {
		return true
	}
	return lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
}

// Run opens the interface at the skill browser, offering setup or adoption
// review first when either has never been seen.
func Run(s *library.Store, repo string) error {
	if err := interactive(); err != nil {
		return err
	}
	a, err := newApp(s, repo)
	if err != nil {
		return err
	}
	if !s.SetupReviewed() {
		a.stack = append(a.stack, newSetupOfferPage(nil))
	} else if repo != "" && a.data.state.Library == "" && !a.data.state.AdoptionReviewed && len(localAdoptions(a)) > 0 {
		a.stack = append(a.stack, newAdoptionOfferPage(a))
	}
	return run(a, nil)
}

// Setup opens the interface directly on the setup review.
func Setup(s *library.Store, repo string, roots []string) error {
	if err := interactive(); err != nil {
		return fmt.Errorf("setup review needs an interactive terminal; use skillverk setup --scan --json")
	}
	a, err := newApp(s, repo)
	if err != nil {
		return err
	}
	a.stack = append(a.stack, newSetupOfferPage(roots))
	return run(a, nil)
}

// Browse opens the interface directly on an already-loaded import source.
func Browse(s *library.Store, repo string, c *library.Collection) error {
	if err := interactive(); err != nil {
		return err
	}
	a, err := newApp(s, repo)
	if err != nil {
		return err
	}
	a.stack = append(a.stack, newSourcePage(a, c))
	return run(a, nil)
}

func isTerminal() bool { return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) }
