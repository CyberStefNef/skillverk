package tui

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"context"
	"fmt"
	"github.com/CyberStefNef/skillverk/internal/library"
	"github.com/CyberStefNef/skillverk/internal/onboarding"
	"github.com/charmbracelet/x/ansi"
	"slices"
	"strings"
)

// menuPage is everything that is about the library and this repository rather
// than about one skill. It is built like the skills screen, and for the same
// reason: the entries down the left, what the highlighted entry can do down
// the right. Enter moves into the pane and acts there.
//
// The menu is the last layer. Choosing a harness, a review provider or an
// import source happens in the pane, so a two-key change never buries the
// list it started from under a screen the user has to climb back out of. The
// entries that have something left to show, such as the skills in a source or
// the setup review, replace the menu rather than stacking on it.
type menuPage struct {
	*listPage
	// focused moves the cursor from the entries into the pane.
	focused bool
	cursor  int
	// editing holds the field of a row that carries a value, such as the import
	// source, while it is being typed into. draft keeps what was typed when the
	// cursor steps off a live field and back onto it.
	editing *textinput.Model
	editRow int
	draft   string
	// live records that the open field is the row itself.
	live bool
	// expanded opens the section a row stands for, in the pane, under the row.
	expanded bool
	// working is the pane row that started the job now running, or -1.
	working int
}

// menuEntry is one row of the menu. An entry either acts in the pane, which is
// the ordinary case, or opens the one screen it needs.
type menuEntry struct {
	name, desc string
	// panel draws this entry's own rows into the pane. It reads the page so a
	// row can expand a section under itself.
	panel func(a *app, p *menuPage) *panel
	// run is for the entries that are a screen of their own, and for the ones
	// that are a single change with nothing to choose.
	run func(a *app) tea.Cmd
}

// panel is the body of an entry: a line of context, rows to act on, and a note
// under them.
type panel struct {
	lead string
	rows []panelRow
	note string
}

type panelRow struct {
	label string
	badge string
	glyph string
	tone  level
	// indent sets a row inside the section above it.
	indent bool
	// edit turns Enter into a text field, for a row that holds a value.
	edit *panelEdit
	run  func(a *app) tea.Cmd
}

type panelEdit struct {
	value, placeholder string
	// live keeps the field open while the cursor is on its row, so a source is
	// typed where it is read rather than after a key that opens a field.
	live   bool
	submit func(a *app, value string) tea.Cmd
}

func newMenuPage(a *app) *menuPage {
	p := &menuPage{listPage: newList(a, "Menu", "", false, true), working: -1}
	p.keys = []key.Binding{bind("m", "close menu")}
	p.fill(a)
	return p
}

func (p *menuPage) fill(a *app) {
	held := ""
	if r, ok := p.focus(); ok {
		held = r.id
	}
	var rows []row
	for _, e := range menuEntries(a) {
		rows = append(rows, row{id: e.name, name: e.name, desc: e.desc, data: e})
	}
	p.setRows(rows)
	for i, r := range rows {
		if r.id == held {
			p.list.Select(i)
			break
		}
	}
}

// entry returns the highlighted entry.
func (p *menuPage) entry() (menuEntry, bool) {
	r, ok := p.focus()
	if !ok {
		return menuEntry{}, false
	}
	e, ok := r.data.(menuEntry)
	return e, ok
}

// panelRows are the rows the pane's cursor can land on.
func (p *menuPage) panelRows(a *app) []panelRow {
	e, ok := p.entry()
	if !ok || e.panel == nil {
		return nil
	}
	return e.panel(a, p).rows
}

// ------------------------------------------------------------------ entries

func menuEntries(a *app) []menuEntry {
	var out []menuEntry
	acts := func(name, desc string, panel func(*app, *menuPage) *panel) {
		out = append(out, menuEntry{name: name, desc: desc, panel: panel})
	}
	opens := func(name, desc string, run func(*app) tea.Cmd) {
		out = append(out, menuEntry{name: name, desc: desc, run: run})
	}
	acts("Import skills", "Add skills from a source, or from those already on this machine", importPanel)
	if a.repo != "" {
		acts("Repository harnesses", "Choose which agents get links to the skills you turn on", harnessPanel)
		if len(localAdoptions(a)) > 0 {
			opens("Share repository skills…", "Move skills that live in this repository into the shared library",
				func(a *app) tea.Cmd { return openAdoptionReview(a) })
		}
		opens("Retry repository changes", "Re-apply changes that previously failed",
			func(a *app) tea.Cmd {
				return a.perform("Retrying", func() ([]library.Result, error) { return a.store.Retry(a.repo) })
			})
	}
	acts("Setup review", "Find the skill installations already on this machine and organise them", setupPanel)
	return out
}

// importPanel is the whole of importing that needs a choice made: where the
// skills come from. The source is typed at the top of the pane, the skills
// already on this machine open under it, and the skills a source holds are
// chosen on the screen that replaces the menu. Nothing else is a screen.
func importPanel(a *app, p *menuPage) *panel {
	pan := &panel{}
	pan.rows = append(pan.rows, panelRow{label: "From a source", badge: "a repository, folder, or archive",
		edit: &panelEdit{
			live:        true,
			value:       p.draft,
			placeholder: "owner/repo, ./path, or https://…",
			submit: func(a *app, value string) tea.Cmd {
				if value == "" {
					return warn("Type a source first.")
				}
				return a.load("Loading "+value, func(ctx context.Context, id uint64) tea.Msg {
					c, err := library.OpenCollectionContext(ctx, value)
					if ctx.Err() != nil && c != nil {
						c.Close()
						return collectionMsg{id: id, err: ctx.Err()}
					}
					return collectionMsg{id: id, c: c, err: err}
				})
			},
		}})
	found := externalSkills(a)
	glyph := "▸"
	if p.expanded {
		glyph = "▾"
	}
	pan.rows = append(pan.rows, panelRow{glyph: glyph, label: "On this machine",
		badge: fmt.Sprintf("%d found", len(found)),
		run: func(a *app) tea.Cmd {
			if len(found) == 0 {
				return warn("Every skill Skillverk can see is already in your library.")
			}
			p.expanded = !p.expanded
			return nil
		}})
	if !p.expanded {
		return pan
	}
	for _, sk := range found {
		pan.rows = append(pan.rows, panelRow{indent: true, label: sk.Name, badge: externalScope(sk),
			run: func(a *app) tea.Cmd { return takeOver(a, sk) }})
	}
	pan.note = "Enter takes a copy into your library. Plugin and inherited files stay where they are."
	return pan
}

// externalSkills are the installations Skillverk did not make.
func externalSkills(a *app) []library.Skill {
	var out []library.Skill
	for _, sk := range a.data.skills {
		if !managed(sk) {
			out = append(out, sk)
		}
	}
	return out
}

// takeOver moves one installation into the shared library, asking first: it
// moves files and replaces them with links, which is not an Enter's worth of
// consequence. A skill installed in more than one place is asked which.
func takeOver(a *app, sk library.Skill) tea.Cmd {
	installs := adoptable(sk)
	switch len(installs) {
	case 0:
		return warn("%s is managed by its plugin or a parent folder. Those files stay there.", sk.Name)
	case 1:
		return push(adoptConfirm(a, sk, installs[0]))
	}
	return push(newAdoptPage(a, sk, installs))
}

// harnessPanel switches the harnesses this repository links into. Each row is
// applied as it is pressed: there is nothing here that needs an apply step, and
// staging a choice hides which harnesses are live right now.
func harnessPanel(a *app, _ *menuPage) *panel {
	p := &panel{lead: "Skills you turn on are linked into every harness selected here."}
	for _, agent := range library.Agents {
		on := slices.Contains(a.data.state.Agents, agent)
		glyph, tone := "·", levelInfo
		if on {
			glyph, tone = "●", levelDone
		}
		p.rows = append(p.rows, panelRow{
			glyph: glyph, tone: tone, label: library.AgentLabel(agent), badge: library.AgentPath(agent),
			run: func(a *app) tea.Cmd { return switchAgent(a, agent, !on) },
		})
	}
	return p
}

// switchAgent adds or removes one harness for this repository.
func switchAgent(a *app, agent string, on bool) tea.Cmd {
	var wanted []string
	for _, existing := range library.Agents {
		keep := slices.Contains(a.data.state.Agents, existing)
		if existing == agent {
			keep = on
		}
		if keep {
			wanted = append(wanted, existing)
		}
	}
	if len(wanted) == 0 {
		return warn("Choose at least one harness. Turn another on before this one off.")
	}
	label := "Linking skills into " + library.AgentLabel(agent)
	if !on {
		label = "Unlinking skills from " + library.AgentLabel(agent)
	}
	return a.performHere(label, func() ([]library.Result, error) { return a.store.SetAgents(a.repo, wanted) })
}

// setupPanel is setup review: the run, and the two settings that decide how it
// runs. They were two menu entries saying the same word, which asked the user
// to guess which of them held the provider.
func setupPanel(a *app, _ *menuPage) *panel {
	provider := onboarding.Resolve(a.data.review.Provider)
	p := &panel{lead: "Skillverk looks over the skills already on this machine and offers to organise them."}
	p.rows = append(p.rows, panelRow{label: "Review this machine",
		run: func(a *app) tea.Cmd { return swap(newSetupOfferPage(nil)) }})
	p.rows = append(p.rows, panelRow{label: "Provider", badge: providerLabel(a.data.review.Provider),
		run: func(a *app) tea.Cmd {
			settings := a.data.review
			settings.Provider = providers[(slices.Index(providers, a.data.review.Provider)+1)%len(providers)]
			return save(a, settings, func(*app) {})
		}})
	model := panelRow{label: "Model", badge: modelLabel(a)}
	if provider == "local" {
		model.run = func(a *app) tea.Cmd {
			return warn("Install Codex or Claude Code, or choose an installed provider first.")
		}
	} else {
		model.edit = &panelEdit{
			value:       a.data.review.Models[provider],
			placeholder: "leave blank for the default",
			submit: func(a *app, value string) tea.Cmd {
				settings := a.data.review
				if settings.Models == nil {
					settings.Models = map[string]string{}
				}
				settings.Models[provider] = strings.TrimSpace(value)
				return save(a, settings, func(*app) {})
			},
		}
	}
	p.rows = append(p.rows, model)
	p.note = "Setup review will run " + reviewSummary(a) + "."
	return p
}

// ------------------------------------------------------------------ drawing

func (p *menuPage) layout(a *app, width, height int) {
	p.list.SetDelegate(rowDelegate{theme: a.theme, blurred: p.focused})
	if split(width) {
		p.listPage.layout(a, listWidth(width), height)
		return
	}
	p.listPage.layout(a, width, height-2)
}

func (p *menuPage) view(a *app) string {
	width := a.contentWidth()
	if !split(width) {
		if p.focused {
			return strings.Join(p.pane(a, width, p.list.Height()+2), "\n")
		}
		line := ""
		if e, ok := p.entry(); ok {
			line = e.desc
		}
		return p.listPage.view(a) + "\n\n" + a.theme.Muted.Render(ansi.Truncate(line, width, "…"))
	}
	return beside(a, p.list.View(), listWidth(width), func(width, height int) []string {
		return p.pane(a, width, height)
	})
}

// pane draws the highlighted entry: what it does, and the rows that do it.
func (p *menuPage) pane(a *app, width, height int) []string {
	t := a.theme
	e, ok := p.entry()
	if !ok {
		return []string{t.Muted.Render("Nothing to do here.")}
	}
	lines := []string{t.Heading.Render(ansi.Truncate(e.name, width, "…")), ""}
	if e.panel == nil {
		lines = append(lines, wrap(t.Muted, e.desc, width, 6)...)
		return clamp(t, lines, height, 0)
	}
	pan := e.panel(a, p)
	if pan.lead != "" {
		lines = append(lines, wrap(t.Muted, pan.lead, width, 3)...)
		lines = append(lines, "")
	}
	cursor := 0
	for i, r := range pan.rows {
		if p.focused && i == p.cursor {
			cursor = len(lines)
		}
		lines = append(lines, p.rowLines(a, r, i, width)...)
	}
	if pan.note != "" {
		lines = append(lines, "", t.Muted.Render(ansi.Truncate(pan.note, width, "…")))
	}
	return clamp(t, lines, height, cursor)
}

// rowLines draws one pane row, with the field it is being edited in under it.
func (p *menuPage) rowLines(a *app, r panelRow, i, width int) []string {
	t := a.theme
	if p.working == i && a.busy != "" {
		return []string{t.Brand.Render(ansi.Truncate("  "+a.spinner.View()+a.busy+"   esc cancel", width, "…"))}
	}
	mark := "  "
	if p.focused && p.cursor == i {
		mark = "› "
	}
	if r.indent {
		mark += "  "
	}
	glyph := ""
	if r.glyph != "" {
		style, _ := t.status(r.tone)
		glyph = style.Render(r.glyph) + " "
	}
	label := r.label
	if r.edit != nil && r.edit.live {
		return p.fieldLines(a, r, i, width)
	}
	if p.editing != nil && p.editRow == i {
		// The field takes its own line: a value shares the row with its label
		// only until the label, the prompt and the text no longer fit in a pane.
		p.editing.SetWidth(max(8, width-4))
		return []string{t.Body.Render(ansi.Truncate(mark+label, width, "…")), "  " + p.editing.View()}
	}
	// The badge sits against the right edge, as it does in every list, so a
	// column of paths or settings can be read down without reading the names.
	head := mark + glyph + ansi.Truncate(label, max(4, width-ansi.StringWidth(mark)-4), "…")
	line := mark + glyph + t.Body.Render(ansi.Truncate(label, max(4, width-ansi.StringWidth(mark)-4), "…"))
	if room := width - ansi.StringWidth(head) - 2; r.badge != "" && room > 8 {
		badge := elide(r.badge, room)
		line += strings.Repeat(" ", max(1, width-ansi.StringWidth(head)-ansi.StringWidth(badge))) + t.Muted.Render(badge)
	}
	if p.focused && p.cursor == i {
		line = t.Cursor.Render(pad(ansi.Strip(line), width))
	}
	return []string{line}
}

// fieldLines draws a live field as what it is: a box to type in, the same size
// and in the same place whether or not the cursor is in it, so moving down the
// pane and back does not shuffle everything under it.
func (p *menuPage) fieldLines(a *app, r panelRow, i, width int) []string {
	t := a.theme
	inner := max(8, width-6)
	label, border := t.Muted, t.faint
	body := t.Muted.Render(ansi.Truncate(value(r.edit.value, r.edit.placeholder), inner, "…"))
	if p.focused && p.cursor == i {
		label, border = t.Heading, t.accent
	}
	if p.editing != nil && p.editRow == i {
		p.editing.SetWidth(max(4, inner-2))
		body = p.editing.View()
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Width(inner).Padding(0, 1).Render(body)
	out := []string{"  " + label.Render(ansi.Truncate(r.label, width-2, "…"))}
	for _, line := range strings.Split(box, "\n") {
		out = append(out, "  "+line)
	}
	return out
}

// value is what a field shows when it is not being typed into.
func value(current, placeholder string) string {
	if strings.TrimSpace(current) == "" {
		return placeholder
	}
	return current
}

// clamp fits a pane to the height beside it. It starts at the top, on the line
// the list beside it starts on: the heading of the pane and the first row of
// the list are the same screen, and a pane that floated in its own space read
// as a second one.
func clamp(t theme, lines []string, height, cursor int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	start := 0
	if cursor >= height-1 {
		start = min(cursor-height+2, len(lines)-height)
	}
	out := append([]string{}, lines[start:start+height]...)
	if start > 0 {
		out[0] = t.Muted.Render("…")
	}
	if start+height < len(lines) {
		out[len(out)-1] = t.Muted.Render("…")
	}
	return out
}

// beside draws a list and a pane side by side, separated by the one rule this
// interface uses for the purpose.
func beside(a *app, listView string, left int, detail func(width, height int) []string) string {
	rows := strings.Split(listView, "\n")
	for len(rows) > 1 && strings.TrimSpace(rows[0]) == "" {
		rows = rows[1:] // the list pads its top; the pane beside it must not
	}
	lines := detail(a.contentWidth()-left-3, len(rows))
	rule := a.theme.Rule.Render(" │ ")
	var out []string
	for i := 0; i < max(len(rows), len(lines)); i++ {
		out = append(out, pad(at(rows, i), left)+rule+at(lines, i))
	}
	return strings.Join(out, "\n")
}

// ------------------------------------------------------------------- keys

// soloTitle drops the breadcrumb. The menu is opened with m from wherever the
// user is, and closed with m again; it is not a step into the screen under it,
// so naming that screen above it says nothing about where they are.
func (p *menuPage) soloTitle() bool { return true }

func (p *menuPage) typing() bool { return p.editing != nil || p.listPage.typing() }

func (p *menuPage) showsProgress() bool { return p.working >= 0 }

func (p *menuPage) bindings() []key.Binding {
	if p.editing != nil {
		if p.live {
			return []key.Binding{bind("enter", "load"), keys.Up, keys.Down, bind("esc", "back to menu")}
		}
		return []key.Binding{bind("enter", "continue"), bind("esc", "cancel")}
	}
	if p.focused {
		return []key.Binding{keys.Up, keys.Down, bind("enter", "choose"), bind("esc", "back to menu"), bind("m", "close menu")}
	}
	return p.listPage.bindings()
}

func (p *menuPage) update(a *app, msg tea.Msg) tea.Cmd {
	if a.busy == "" {
		p.working = -1
	}
	if v, ok := msg.(collectionMsg); ok {
		return p.loaded(a, v)
	}
	if _, ok := msg.(reloadMsg); ok {
		// Whatever the pane was holding describes a state that has moved on.
		p.editing = nil
		p.fill(a)
		p.cursor = min(p.cursor, max(0, len(p.panelRows(a))-1))
		p.sync(a)
		return nil
	}
	if p.editing != nil {
		return p.edit(a, msg)
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok || p.typing() {
		return p.listPage.update(a, msg)
	}
	if p.focused {
		return p.act(a, k)
	}
	switch k.String() {
	case "m":
		return pop
	case "enter", "right", "l":
		return p.choose(a)
	}
	return p.listPage.update(a, msg)
}

// loaded opens the source a typed row fetched, in place of the menu: the menu
// has nothing more to ask, so it is not a screen worth keeping underneath.
func (p *menuPage) loaded(a *app, v collectionMsg) tea.Cmd {
	if v.id != a.jobID {
		if v.c != nil {
			v.c.Close()
		}
		return nil
	}
	a.stop()
	p.working = -1
	if v.err != nil {
		return failure(v.err)
	}
	return swap(newSourcePage(a, v.c))
}

// choose acts on the highlighted entry: the ones with a panel take the cursor
// into it, and the two that are a screen of their own open it.
func (p *menuPage) choose(a *app) tea.Cmd {
	e, ok := p.entry()
	if !ok {
		return nil
	}
	if e.panel == nil {
		return e.run(a)
	}
	p.focused, p.cursor = true, 0
	p.sync(a)
	return nil
}

// sync opens or closes a live field as the cursor arrives on or leaves its row.
// A live field is the row: there is no key that opens it, and what was typed
// survives a trip down the rows and back.
func (p *menuPage) sync(a *app) {
	rows := p.panelRows(a)
	if p.focused && p.cursor < len(rows) {
		if e := rows[p.cursor].edit; e != nil && e.live {
			if p.editing == nil || p.editRow != p.cursor {
				p.open(a, *e, p.cursor)
			}
			return
		}
	}
	if p.editing == nil || p.editRow >= len(rows) {
		return
	}
	if e := rows[p.editRow].edit; e != nil && e.live {
		p.draft, p.editing = p.editing.Value(), nil
	}
}

// open puts a field on one row, ready to type into.
func (p *menuPage) open(a *app, e panelEdit, index int) {
	in := textinput.New()
	in.Prompt = "› "
	in.Placeholder = e.placeholder
	in.SetValue(e.value)
	in.SetStyles(a.theme.Input)
	in.CursorEnd()
	in.Focus()
	p.editing, p.editRow, p.live = &in, index, e.live
}

// act handles the keys that belong to the pane while it holds the cursor.
func (p *menuPage) act(a *app, k tea.KeyPressMsg) tea.Cmd {
	rows := p.panelRows(a)
	p.cursor = min(p.cursor, max(0, len(rows)-1))
	switch k.String() {
	case "esc", "left", "h":
		p.focused = false
		p.sync(a)
	case "up", "k":
		p.cursor = max(0, p.cursor-1)
		p.sync(a)
	case "down", "j":
		p.cursor = min(len(rows)-1, p.cursor+1)
		p.sync(a)
	case "m":
		return pop
	case "enter", " ", "space":
		if p.cursor >= len(rows) {
			return nil
		}
		cmd := p.run(a, rows[p.cursor])
		p.sync(a)
		return cmd
	}
	return nil
}

// run acts on one pane row: a value opens its field, anything else runs at
// once.
func (p *menuPage) run(a *app, r panelRow) tea.Cmd {
	if r.edit != nil {
		p.open(a, *r.edit, p.cursor)
		return nil
	}
	if r.run == nil {
		return nil
	}
	// Remember the row before running it, so whatever it starts reports its
	// progress here rather than at the foot of the screen.
	p.working = p.cursor
	cmd := r.run(a)
	if a.busy == "" {
		p.working = -1
	}
	return cmd
}

// edit routes keys to the field a row opened.
func (p *menuPage) edit(a *app, msg tea.Msg) tea.Cmd {
	rows := p.panelRows(a)
	live := p.editRow < len(rows) && rows[p.editRow].edit != nil && rows[p.editRow].edit.live
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "up", "down":
			if live {
				return p.act(a, k) // step out of the field, onto the next row
			}
		case "esc":
			if live {
				// The field is the row, so Esc leaves the pane rather than the
				// field, and what was typed is kept for the next visit.
				p.draft, p.editing, p.focused = p.editing.Value(), nil, false
				return nil
			}
			p.editing = nil
			return note("Cancelled.")
		case "enter":
			value := strings.TrimSpace(p.editing.Value())
			if !live {
				p.editing = nil
			} else {
				p.draft = value
			}
			if p.editRow >= len(rows) || rows[p.editRow].edit == nil {
				return nil
			}
			// Whatever the field starts reports itself on the row it was typed
			// into, not at the foot of the screen.
			p.working = p.editRow
			cmd := rows[p.editRow].edit.submit(a, value)
			if a.busy == "" {
				p.working = -1
			}
			return cmd
		}
	}
	if v, ok := msg.(tea.PasteMsg); ok {
		msg = tea.PasteMsg{Content: strings.ReplaceAll(library.Clean(v.Content), "\n", " ")}
	}
	in, cmd := p.editing.Update(msg)
	p.editing = &in
	return cmd
}
