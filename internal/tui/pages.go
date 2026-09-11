package tui

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"fmt"
	"github.com/CyberStefNef/skillverk/internal/library"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

// ---------------------------------------------------------------- list page

// listPage is the workhorse screen: a scrollable list of rows with optional
// live search, optional checkboxes, and a few lines of context underneath.
type listPage struct {
	head, sub string
	list      list.Model
	keys      []key.Binding
	closer    func()

	// notes draws context under the list, such as counts or the focused path.
	notes func(a *app, p *listPage) []string
	// detail enables the same list and detail layout as the home screen.
	detail func(a *app, p *listPage, width int) []string
	// enter, toggle and other handle the keys this screen adds.
	enter  func(a *app, p *listPage) tea.Cmd
	toggle func(a *app, p *listPage) tea.Cmd
	other  func(a *app, p *listPage, k tea.KeyPressMsg) tea.Cmd
	// refreshed rebuilds rows after the library changes underneath.
	refreshed func(a *app, p *listPage) tea.Cmd
	// back overrides Esc, which otherwise returns to the previous screen.
	back func(a *app, p *listPage) tea.Cmd
}

func newList(a *app, head, sub string, showDesc, search bool) *listPage {
	l := list.New(nil, rowDelegate{theme: a.theme, showDesc: showDesc}, a.contentWidth(), 10)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(search)
	l.SetShowFilter(search)
	l.DisableQuitKeybindings()
	l.Styles = a.theme.List
	l.FilterInput.Prompt = "Search: "
	l.FilterInput.SetStyles(a.theme.Input)
	l.KeyMap.PrevPage.SetEnabled(false)
	l.KeyMap.NextPage.SetEnabled(false)
	l.KeyMap.GoToStart.SetEnabled(false)
	l.KeyMap.GoToEnd.SetEnabled(false)
	return &listPage{head: head, sub: sub, list: l}
}

func (p *listPage) setRows(rows []row) {
	index := p.list.Index()
	p.list.SetItems(items(rows))
	if index < len(rows) {
		p.list.Select(index)
	}
}

// focus returns the row under the cursor.
func (p *listPage) focus() (row, bool) {
	r, ok := p.list.SelectedItem().(row)
	return r, ok
}

// rows returns every row, filtered or not, so screens can read selections
// the user made before narrowing the search.
func (p *listPage) rows() []row {
	out := make([]row, 0, len(p.list.Items()))
	for _, item := range p.list.Items() {
		if r, ok := item.(row); ok {
			out = append(out, r)
		}
	}
	return out
}

func (p *listPage) title() string { return p.head }
func (p *listPage) lead() string {
	if p.list.FilterState() == list.FilterApplied {
		return "Showing matches for “" + p.list.FilterValue() + "” · Esc clears"
	}
	return p.sub
}
func (p *listPage) typing() bool { return p.list.SettingFilter() }
func (p *listPage) close() {
	if p.closer != nil {
		p.closer()
	}
}

func (p *listPage) layout(a *app, width, height int) {
	notes := 0
	if p.notes != nil {
		if lines := p.notes(a, p); len(lines) > 0 {
			notes = len(lines) + 1
		}
	}
	if p.detail != nil {
		p.list.SetDelegate(rowDelegate{theme: a.theme, showDesc: !split(width)})
		if split(width) {
			width = listWidth(width)
		}
	}
	p.list.SetSize(width, max(3, height-notes))
}

func (p *listPage) view(a *app) string {
	out := p.list.View()
	if p.detail != nil && split(a.contentWidth()) {
		left := listWidth(a.contentWidth())
		rows := strings.Split(out, "\n")
		detail := p.detail(a, p, a.contentWidth()-left-3)
		for i := range rows {
			rows[i] = pad(rows[i], left) + a.theme.Rule.Render(" │ ") + ansi.Truncate(at(detail, i), a.contentWidth()-left-3, "…")
		}
		out = strings.Join(rows, "\n")
	}
	if p.notes == nil {
		return out
	}
	lines := p.notes(a, p)
	if len(lines) == 0 {
		return out
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, a.contentWidth(), "…")
	}
	return out + "\n\n" + strings.Join(lines, "\n")
}

func (p *listPage) bindings() []key.Binding {
	b := []key.Binding{keys.Up, keys.Down}
	if p.list.FilteringEnabled() {
		b = append(b, keys.Search)
	}
	if p.toggle != nil {
		b = append(b, keys.Toggle)
	}
	if p.enter != nil {
		b = append(b, keys.Choose)
	}
	b = append(b, p.keys...)
	return append(b, keys.Back)
}

func (p *listPage) update(a *app, msg tea.Msg) tea.Cmd {
	if _, ok := msg.(reloadMsg); ok && p.refreshed != nil {
		return p.refreshed(a, p)
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok || p.list.SettingFilter() {
		return p.delegate(msg)
	}
	switch {
	case key.Matches(k, keys.Back):
		if p.list.FilterState() != list.Unfiltered {
			return p.delegate(msg)
		}
		if p.back != nil {
			return p.back(a, p)
		}
		return pop
	case key.Matches(k, keys.Toggle) && p.toggle != nil:
		return p.toggle(a, p)
	case key.Matches(k, keys.Choose) && p.enter != nil:
		return p.enter(a, p)
	}
	if p.other != nil {
		if cmd := p.other(a, p, k); cmd != nil {
			return cmd
		}
	}
	return p.delegate(msg)
}

func (p *listPage) delegate(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(msg)
	return cmd
}

// ----------------------------------------------------------------- doc page

// docPage shows scrollable read-only text: skill details, scan warnings, the
// full text of the last operation.
type docPage struct {
	head, sub, body string
	view_           viewport.Model
}

func newDocPage(head, sub, body string) *docPage {
	return &docPage{head: head, sub: sub, body: body, view_: viewport.New()}
}

func (p *docPage) title() string { return p.head }
func (p *docPage) lead() string  { return p.sub }
func (p *docPage) typing() bool  { return false }

func (p *docPage) layout(_ *app, width, height int) {
	p.view_.SetWidth(width)
	p.view_.SetHeight(max(3, height-1))
	p.view_.SetContent(ansi.Hardwrap(library.Clean(p.body), width, true))
}

func (p *docPage) view(a *app) string {
	scroll := ""
	if p.view_.TotalLineCount() > p.view_.VisibleLineCount() {
		scroll = a.theme.Muted.Render(fmt.Sprintf("%d%% · %d lines", int(p.view_.ScrollPercent()*100), p.view_.TotalLineCount()))
	}
	return p.view_.View() + "\n" + scroll
}

func (p *docPage) bindings() []key.Binding {
	return []key.Binding{keys.Up, keys.Down, keys.Back}
}

func (p *docPage) update(a *app, msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyPressMsg); ok && key.Matches(k, keys.Back) {
		return pop
	}
	var cmd tea.Cmd
	p.view_, cmd = p.view_.Update(msg)
	return cmd
}

// ------------------------------------------------------------- confirm page

// confirmPage states the exact effects of a change and defaults to cancelling.
// Confirmation is a deliberate move, never a stray Enter.
type confirmPage struct {
	head, sub, body string
	verb            string
	view_           viewport.Model
	choice          int
	action          func(a *app) tea.Cmd
	// no names the other answer. A question that destroys nothing is not a
	// confirmation: declining it is an answer, not a cancellation, and the
	// answer that gets on with the work is the one under the cursor.
	no string
}

// newQuestionPage asks something that takes nothing away, so it defaults to
// yes and says so in the ordinary colours rather than the warning ones.
func newQuestionPage(head, verb, no, body string, action func(a *app) tea.Cmd) *confirmPage {
	p := newConfirmPage(head, verb, body, action)
	p.sub, p.choice, p.no = "", 1, no
	return p
}

func newConfirmPage(head, verb, body string, action func(a *app) tea.Cmd) *confirmPage {
	return &confirmPage{head: head, sub: "Read the effects below, then confirm or cancel.",
		body: body, verb: verb, view_: viewport.New(), action: action}
}

func (p *confirmPage) title() string { return p.head }
func (p *confirmPage) lead() string  { return p.sub }
func (p *confirmPage) typing() bool  { return false }

func (p *confirmPage) layout(_ *app, width, height int) {
	p.view_.SetWidth(width)
	p.view_.SetHeight(max(3, height-2))
	p.view_.SetContent(ansi.Hardwrap(library.Clean(p.body), width, true))
}

func (p *confirmPage) view(a *app) string {
	t := a.theme
	no := "Cancel"
	if p.no != "" {
		no = p.no
	}
	cancel, confirm := t.TabOff.Render(no), t.TabOff.Render(p.verb)
	if p.choice == 0 {
		cancel = t.TabOn.Render(no)
	} else if p.no != "" {
		confirm = t.TabOn.Render(p.verb)
	} else {
		confirm = lipgloss.NewStyle().Foreground(t.onFocus).Background(t.warn).Bold(true).Padding(0, 1).Render(p.verb)
	}
	return p.view_.View() + "\n\n" + cancel + "  " + confirm
}

func (p *confirmPage) bindings() []key.Binding {
	no := bind("n", "cancel")
	if p.no != "" {
		no = bind("n", strings.ToLower(p.no))
	}
	return []key.Binding{
		bind("←/→", "choose", "left", "right", "tab", "h", "l"),
		keys.Choose, keys.Yes, no, keys.Up, keys.Down, keys.Cancel,
	}
}

func (p *confirmPage) update(a *app, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		p.view_, cmd = p.view_.Update(msg)
		return cmd
	}
	switch k.String() {
	case "left", "right", "tab", "h", "l":
		p.choice = 1 - p.choice
		return nil
	case "y":
		return p.run(a)
	case "n", "esc":
		return p.decline()
	case "enter":
		if p.choice == 1 {
			return p.run(a)
		}
		return p.decline()
	}
	var cmd tea.Cmd
	p.view_, cmd = p.view_.Update(msg)
	return cmd
}

func (p *confirmPage) run(a *app) tea.Cmd {
	if p.action == nil {
		return pop
	}
	return p.action(a)
}

// decline answers no, which for a question is an answer and not a cancellation.
func (p *confirmPage) decline() tea.Cmd {
	if p.no != "" {
		return tea.Sequence(pop, note("%s.", p.no))
	}
	return abort()
}

func abort() tea.Cmd { return tea.Sequence(pop, note("Cancelled.")) }

// --------------------------------------------------------------- input page

// inputPage collects one line of text: an import source, a model name.
type inputPage struct {
	head, sub string
	hints     []string
	input     textinput.Model
	submit    func(a *app, value string) tea.Cmd
}

func newInputPage(a *app, head, sub, value, placeholder string, submit func(*app, string) tea.Cmd) *inputPage {
	in := textinput.New()
	in.Prompt = "› "
	in.Placeholder = placeholder
	in.SetValue(value)
	in.SetStyles(a.theme.Input)
	in.CursorEnd()
	in.Focus()
	return &inputPage{head: head, sub: sub, input: in, submit: submit}
}

func (p *inputPage) title() string { return p.head }
func (p *inputPage) lead() string  { return p.sub }
func (p *inputPage) typing() bool  { return true }

func (p *inputPage) layout(_ *app, width, _ int) { p.input.SetWidth(max(8, width-2)) }

func (p *inputPage) view(a *app) string {
	out := []string{p.input.View(), ""}
	for _, hint := range p.hints {
		out = append(out, a.theme.Muted.Render(ansi.Truncate(hint, a.contentWidth(), "…")))
	}
	return strings.Join(out, "\n")
}

func (p *inputPage) bindings() []key.Binding {
	return []key.Binding{bind("enter", "continue"), keys.Cancel}
}

func (p *inputPage) update(a *app, msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc":
			return pop
		case "enter":
			return p.submit(a, strings.TrimSpace(p.input.Value()))
		}
	}
	if v, ok := msg.(tea.PasteMsg); ok {
		msg = tea.PasteMsg{Content: strings.ReplaceAll(library.Clean(v.Content), "\n", " ")}
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return cmd
}
