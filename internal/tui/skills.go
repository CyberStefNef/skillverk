package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/CyberStefNef/skillverk/internal/library"
	"github.com/charmbracelet/x/ansi"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// skillsPage is the main screen: the skills down the left, everything about
// the highlighted one down the right. The list stays one line per skill so it
// can be scanned. The detail pane answers the questions a name cannot: where
// the skill applies and where it came from.
type skillsPage struct {
	external bool
	*listPage
	// focused moves the cursor from the list into the pane, where the skill's
	// actions become a list of their own. Enter goes in, Esc comes back, and
	// nothing is stacked on top of the screen to do it.
	focused bool
	action  int

	// working is the pane row that started the job now running, or -1. Work
	// begun from a row reports itself on that row: a spinner at the foot of the
	// screen, far from the key that was pressed, makes the user find the
	// feedback for their own action.
	working int
	// upstream holds search results while they are open under the actions, for
	// the skill named in upstreamFor. They expand in place rather than opening
	// a screen, which would lose the row the search started from.
	upstream    []library.Candidate
	upstreamFor string
}

func newSkillsPage(a *app) *skillsPage {
	// No lead line: the footer already names every key this screen uses.
	p := &skillsPage{listPage: newList(a, "Skills", "", false, true), working: -1}
	p.keys = []key.Binding{keys.Actions}
	p.toggle = func(a *app, _ *listPage) tea.Cmd { return p.activate(a) }
	p.enter = func(a *app, _ *listPage) tea.Cmd { return p.focus_(a) }
	p.refreshed = func(a *app, _ *listPage) tea.Cmd { p.fill(a); return nil }
	p.fill(a)
	return p
}

// skill returns the focused skill, if any.
func (p *skillsPage) skill() *library.Skill {
	r, ok := p.focus()
	if !ok {
		return nil
	}
	sk, _ := r.data.(library.Skill)
	return &sk
}

// fill lists every skill Skillverk can act on, the ones that are on for this
// repository first. Installations it does not manage are inventory rather than
// choices and have their own screen, so they stay out.
func (p *skillsPage) fill(a *app) {
	// Turning a skill on moves it up the list, so the cursor follows the skill
	// rather than the position: acting on a row must not hand the next key
	// press to a different skill.
	held := ""
	if r, ok := p.focus(); ok {
		held = r.id
	}
	var rows []row
	for _, sk := range a.data.skills {
		if managed(sk) == p.external {
			continue
		}
		r := row{id: sk.Name, name: sk.Name, desc: sk.Description, data: sk, check: checkOff}
		r.badge, r.tone = badgeFor(sk)
		if p.external {
			r.check = checkNone
		} else if on(sk) {
			r.check = checkOn
		}
		rows = append(rows, r)
	}
	p.setRows(rows)
	for i, r := range rows {
		if r.id == held {
			p.list.Select(i)
			break
		}
	}
}

// agents are the harness columns of the matrix: the ones this repository has
// enabled. Outside a repository there is nothing to link to, so there are none
// and the list falls back to a single on/off glyph.
func (p *skillsPage) agents(a *app) []string {
	if a.repo == "" || p.external {
		return nil
	}
	return a.data.state.Agents
}

// managed reports whether Skillverk decides where this skill applies: it is in
// the library, on for this repository, or owned by the repository itself.
func managed(sk library.Skill) bool {
	return sk.Entry != nil || sk.Selected || sk.HasScope("project")
}

// on reports whether the skill applies to this repository right now.
// Repository-owned skills always do; they ship with the code.
func on(sk library.Skill) bool { return sk.Selected || sk.HasScope("project") }

// badgeFor names only what the checkbox cannot say, so a badge in the list
// always means "this row is not the ordinary case".
func badgeFor(sk library.Skill) (string, level) {
	switch {
	case sk.Status() == "partial" || sk.Problem != "":
		return "needs attention", levelWarn
	case !managed(sk):
		return externalScope(sk), levelInfo
	case sk.HasScope("project"):
		return "repo", levelInfo
	}
	return "", levelInfo
}

func externalScope(sk library.Skill) string {
	for _, scope := range []string{"plugin", "global", "inherited"} {
		if sk.HasScope(scope) {
			return scope
		}
	}
	return "external"
}

// activate turns the focused skill on or off for this repository.
func (p *skillsPage) activate(a *app) tea.Cmd {
	sk := p.skill()
	switch {
	case sk == nil:
		return nil
	case a.repo == "":
		return warn("Open a Git working tree to turn skills on.")
	case sk.HasScope("project"):
		return warn("%s is repository-owned; it is always available here.", sk.Name)
	case sk.Entry == nil && !sk.Selected:
		return warn("%s is installed elsewhere. Press Enter for ownership actions.", sk.Name)
	}
	name, turnOn := sk.Name, !sk.Selected
	label := "Turning on " + name
	if !turnOn {
		label = "Turning off " + name
	}
	return a.perform(label, func() ([]library.Result, error) { return a.store.Select(a.repo, []string{name}, turnOn) })
}

// ------------------------------------------------------------------ drawing

// listWidth is how much of the frame the names take. The rest is detail.
func listWidth(width int) int { return max(24, min(40, width*2/5)) }

// split reports whether the frame is wide enough to show detail beside the
// list rather than under it.
func split(width int) bool { return width >= 72 }

func (p *skillsPage) layout(a *app, width, height int) {
	// One cursor at a time: the list goes quiet while the pane holds it.
	p.list.SetDelegate(rowDelegate{theme: a.theme, blurred: p.focused})
	if split(width) {
		p.listPage.layout(a, listWidth(width), height)
		return
	}
	p.listPage.layout(a, width, height-2)
}

func (p *skillsPage) view(a *app) string {
	width := a.contentWidth()
	if !split(width) {
		if p.external && p.focused {
			return strings.Join(p.detail(a, width, p.list.Height()), "\n")
		}
		// Too narrow for a pane: keep the list, and say the one thing about
		// the highlighted skill that the list cannot, harnesses included since
		// there is nowhere else for them to go.
		line := p.state(a)
		if sk := p.skill(); sk != nil && a.repo != "" {
			var linked []string
			for _, agent := range p.agents(a) {
				if sk.States[agent] == "active" {
					linked = append(linked, library.AgentLabel(agent))
				}
			}
			if len(linked) > 0 {
				line += " · " + strings.Join(linked, ", ")
			}
		}
		return p.listPage.view(a) + "\n\n" + a.theme.Muted.Render(ansi.Truncate(line, width, "…"))
	}
	return beside(a, p.list.View(), listWidth(width), func(width, height int) []string {
		return p.detail(a, width, height)
	})
}

func at(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

// detail draws the pane: what the skill is, where it applies, and where it came
// from. It is capped at the height of the list beside it.
func (p *skillsPage) detail(a *app, width, height int) []string {
	t := a.theme
	sk := p.skill()
	if sk == nil {
		return []string{t.Muted.Render(ansi.Truncate(p.empty(a), width, "…"))}
	}
	// No state line: the harness lines below say where the skill applies, and
	// the list beside them says whether it is on at all.
	lines := []string{t.Heading.Render(ansi.Truncate(sk.Name, width, "…")), ""}
	if matrix := p.harnesses(a, *sk, width); len(matrix) > 0 {
		lines = append(lines, matrix...)
		lines = append(lines, "")
	}
	if p.external {
		message := "Enter to bring this installation into your library."
		if len(adoptable(*sk)) == 0 {
			message = "Managed by its plugin or parent folder. These files stay there."
		}
		lines = append(lines, wrap(t.Muted, message, width, 3)...)
		lines = append(lines, "")
	}
	if p.external {
		for _, install := range sk.Installations {
			lines = append(lines, field(t, install.Scope, home(library.Clean(install.Path)), width)...)
		}
	} else {
		for _, f := range fields(a, *sk) {
			lines = append(lines, field(t, f.label, f.value, width)...)
		}
	}
	if sk.Problem != "" {
		lines = append(lines, wrap(t.Warn, library.Clean(sk.Problem), width, 3)...)
	}
	// The description is the one part whose height depends on the skill, so it
	// sits at the foot of the pane. Everything above it keeps its place as the
	// cursor moves, and nothing on screen jumps.
	// Everything above is the same height for every skill, so what follows it
	// never moves. That foot of the pane holds the description while the list
	// has the cursor, and the skill's actions once the pane takes it.
	lines = append(lines, "")
	if p.focused {
		return append(lines, p.actionLines(a, width)...)
	}
	const descriptionLines = 4
	desc := wrap(t.Muted, library.Clean(sk.Description), width, descriptionLines)
	lines = append(lines, desc...)
	if gap := descriptionLines - len(desc); gap > 0 {
		lines = append(lines, make([]string, gap)...)
	}
	if len(lines) > height && height > 0 {
		lines = append(lines[:max(0, height-1)], t.Muted.Render("…"))
	}
	return lines
}

// harnesses is the matrix: one line per harness this repository could link to,
// saying whether it holds this skill and where. Harnesses that are switched off
// but hold files anyway are listed too, because that is exactly the case a
// summary hides.
func (p *skillsPage) harnesses(a *app, sk library.Skill, width int) []string {
	t := a.theme
	enabled := p.agents(a)
	if a.repo == "" || p.external {
		return nil
	}
	var show []string
	for _, agent := range library.Agents {
		if slices.Contains(enabled, agent) {
			show = append(show, agent)
		}
	}
	if len(show) == 0 {
		return []string{t.Muted.Render("No harnesses enabled for this repository.")}
	}
	label := 0
	for _, agent := range show {
		label = max(label, ansi.StringWidth(library.AgentLabel(agent)))
	}
	head := "Harnesses"
	if p.focused {
		head += " · space links or unlinks this skill"
	}
	lines := []string{t.Muted.Render(ansi.Truncate(head, width, "…"))}
	for i, agent := range show {
		if p.focused && p.working == i && a.busy != "" {
			lines = append(lines, t.Brand.Render(ansi.Truncate("  "+a.spinner.View()+a.busy+"   esc cancel", width, "…")))
			continue
		}
		glyph, tone, detail := harnessState(sk, agent)
		style, _ := t.status(tone)
		mark := "  "
		if p.focused && p.action == i {
			mark = "› "
		}
		line := mark + style.Render(glyph) + " " + t.Body.Render(pad(library.AgentLabel(agent), label))
		room := width - label - 5
		if room > 8 {
			line += " " + t.Muted.Render(elide(detail, room))
		}
		if p.focused && p.action == i {
			plain := ansi.Strip(line)
			plain = strings.Replace(plain, glyph, style.Render(glyph), 1)
			line = t.Cursor.Render(pad(plain, width))
		}
		lines = append(lines, line)
	}
	return lines
}

// harnessState reads one square of the matrix: its glyph, how loudly to say it,
// and the path or reason behind it.
func harnessState(sk library.Skill, agent string) (string, level, string) {
	switch state := sk.States[agent]; state {
	case "active":
		return "●", levelDone, library.AgentPath(agent) + "/" + sk.Name
	case "external":
		// A repository-owned skill's files are the skill, committed here, so
		// the harness reads them without Skillverk linking anything.
		if sk.HasScope("project") {
			return "●", levelDone, library.AgentPath(agent) + "/" + sk.Name + " (in Git)"
		}
		return "○", levelInfo, "files Skillverk did not place"
	case "off", "":
		return "·", levelInfo, "not linked"
	case "missing":
		return "!", levelWarn, "link missing; retry repository changes"
	case "unexpected managed link":
		return "!", levelWarn, "stale link left behind"
	case "missing central content":
		return "!", levelWarn, "library copy is gone"
	default:
		return "!", levelWarn, state
	}
}

// actionLines draw the pane's own list, which only appears once the pane holds
// the cursor: browsing stays quiet, and Enter has something to show for itself.
func (p *skillsPage) actionLines(a *app, width int) []string {
	if !p.focused {
		return nil
	}
	t := a.theme
	offset := len(p.agents(a))
	actions := p.actions(a)
	lines := []string{t.Muted.Render("Actions · enter runs one, esc returns to the list")}
	for i, act := range actions {
		lines = append(lines, p.paneRow(a, act.name, i+offset, width, act.danger))
	}
	if sk := p.skill(); sk == nil || p.upstreamFor != sk.Name || len(p.upstream) == 0 {
		return lines
	}
	// The search results open under the action that asked for them, indented so
	// they read as its answer rather than as more actions.
	lines = append(lines, "", t.Muted.Render(ansi.Truncate("Found in your sources · enter records one", width, "…")))
	for i, c := range p.upstream {
		desc, badge, tone := proves(c)
		style, _ := t.status(tone)
		label := join(repoURL(c.Upstream), c.UpstreamPath)
		lines = append(lines, p.paneRow(a, label, offset+len(actions)+i, width, false))
		lines = append(lines, "    "+style.Render(badge)+t.Muted.Render(" · "+
			elide(desc, max(1, width-6-ansi.StringWidth(badge)))))
	}
	return lines
}

// paneRow draws one selectable row, and reports its own progress when it is the
// row that started the work now running.
func (p *skillsPage) paneRow(a *app, label string, index, width int, danger bool) string {
	t := a.theme
	if p.working == index && a.busy != "" {
		busy := a.spinner.View() + a.busy + "   esc cancel"
		return t.Brand.Render(ansi.Truncate("  "+busy, width, "…"))
	}
	name := ansi.Truncate(label, width-2, "…")
	if index == p.action {
		return t.Cursor.Render(pad("› "+name, width))
	}
	// What destroys something looks like it. The cursor style wins while the
	// row is highlighted, so this colours the list as it is scanned.
	if danger {
		style, _ := t.status(levelWarn)
		return style.Render("  " + name)
	}
	return t.Body.Render("  " + name)
}

// state summarises the skill in one word or two, for the narrow layout that
// has no room for the detail pane. The pane itself needs no such line: its
// harness lines say where the skill applies.
func (p *skillsPage) state(a *app) string {
	sk := p.skill()
	switch {
	case sk == nil:
		return p.empty(a)
	case sk.Status() == "partial" || sk.Problem != "":
		return "Needs attention"
	case p.external:
		return "Installed in a " + externalScope(*sk) + " folder"
	case sk.HasScope("project"):
		return "Owned by this repository, always on here"
	case sk.Selected:
		return "On here"
	case a.repo == "":
		return "In your library"
	default:
		return "Off here"
	}
}

// labelled is one fact under the description.
type labelled struct{ label, value string }

// fields are those facts in the order someone asks for them: how the skill is
// stored, and where it came from.
//
// No paths. A pane-width path is either a constant prefix or an opaque entry
// ID once it has been truncated to fit, so it never repaid the line it took.
// The harness lines above already say where a link landed, and skillverk path
// prints the library copy for the rare moment someone needs to open it.
func fields(a *app, sk library.Skill) []labelled {
	var out []labelled
	// Every skill gets the same rows in the same order, an em dash where it has
	// nothing to say, so the block is the same height for all of them and the
	// description below it never moves.
	add := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			value = "—"
		}
		out = append(out, labelled{label, library.Clean(value)})
	}
	switch sk.Ownership() {
	case "repository":
		add("Storage", "This repository, in Git")
	case "shared":
		add("Storage", "Shared library")
	default:
		add("Storage", "Outside Skillverk")
	}
	from := ""
	if sk.Entry != nil {
		from = origin(*sk.Entry)
	}
	add("From", from)
	return out
}

// sourceProblem reports why a skill cannot be checked or updated, as a clause
// to put after its name and a paragraph to read behind it. An empty short
// clause means the source is worth trying.
//
// This exists because the honest answer is a sentence, not a path. Telling
// someone "source no longer exists: …/.git/skillverk-originals/b45c…/original"
// names a directory they never created, for a reason it does not give.
func sourceProblem(e library.Entry) (string, string) {
	if e.Upstream != "" {
		return "", ""
	}
	if from := adoptedFrom(e.Source); from != "" {
		return "was adopted, so it has no source to check", strings.Join([]string{
			"Skillverk took this skill from an installation that was sitting in",
			home(from) + ".",
			"",
			"Adopting copies what is on disk. It does not learn where that copy was",
			"published, so there is no repository to compare against and nothing to",
			"fetch. That is true of every skill whose From line ends in · adopted.",
			"",
			"Find its source, in this skill's actions, searches the sources you",
			"already follow for where it was published, and records the link once you",
			"pick one. After that, checking and updating work normally.",
			"",
			"If you already know the repository, skillverk add SOURCE --replace sets",
			"it directly.",
		}, "\n")
	}
	if internalPath(e.Source) {
		return "has no source to check", "This skill's recorded source is one of Skillverk's own working directories, which is not a place anything can be fetched from."
	}
	if path := library.Expand(e.Source); filepath.IsAbs(path) && !library.Exists(path) {
		return "no longer has a source", strings.Join([]string{
			"This skill was imported from",
			home(path) + ",",
			"which is not there any more.",
			"",
			"Your library copy is safe and keeps working. Only checking and updating",
			"need the source. Point it at a new one with:",
			"",
			"    skillverk add SOURCE --replace",
		}, "\n")
	}
	return "", ""
}

// origin says where a library skill came from in the terms its owner used: the
// repository it tracks, or the folder it was imported from. Skillverk's own
// staging and backup directories are implementation detail, not provenance, so
// they are described rather than printed.
func origin(e library.Entry) string {
	if url := repoURL(e.Upstream); url != "" {
		return join(e.Upstream, e.UpstreamPath)
	}
	if from := adoptedFrom(e.Source); from != "" {
		return home(from) + " · adopted"
	}
	if internalPath(e.Source) {
		return "an installation Skillverk adopted"
	}
	return join(home(e.Source), e.Subpath)
}

// adoptedFrom recovers where an adopted skill was taken from. Skillverk holds
// the untouched original in custody beside the place it came from, inside a
// repository's .git directory, or in a sibling of the harness skills directory
// it sat in, so the custody path still names that place long after the
// original itself has been deleted. Saying "pdx-cif" is worth a line; saying
// "an installation Skillverk adopted" never was.
//
// Import staging is deliberately not recovered: a .import- directory is a
// temporary of Skillverk's own, and the folder behind it is already the entry
// source in every case where one exists.
func adoptedFrom(source string) string {
	parts := strings.Split(filepath.ToSlash(source), "/")
	for i, part := range parts {
		head := parts[:i]
		switch {
		case part == "skillverk-originals":
			// <repo>/.git/skillverk-originals/<id>/original
			if len(head) > 0 && head[len(head)-1] == ".git" {
				head = head[:len(head)-1]
			}
		case strings.HasPrefix(part, ".skillverk-original-"):
			// <harness skills directory>/.skillverk-original-<n>/original
		default:
			continue
		}
		if len(head) == 0 {
			return ""
		}
		return filepath.FromSlash(strings.Join(head, "/"))
	}
	return ""
}

// repoURL trims a clone URL down to the part people recognise.
func repoURL(upstream string) string {
	if upstream == "" {
		return ""
	}
	url := strings.TrimSuffix(upstream, ".git")
	for _, prefix := range []string{"https://", "http://", "ssh://", "git@"} {
		url = strings.TrimPrefix(url, prefix)
	}
	return strings.Replace(url, ":", "/", 1)
}

// internalPath reports a path Skillverk made for its own bookkeeping: the
// staging directories imports run through, and the backups a migration keeps.
func internalPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, ".skillverk-original-") || strings.HasPrefix(part, ".import-") ||
			part == "skillverk-originals" {
			return true
		}
	}
	return false
}

// join displays a complete source path. GitHub sources point to the skill's
// directory on the recorded ref, or the default branch when no ref was saved.
func join(source, subpath string) string {
	normalized := source
	for _, prefix := range []string{"git@github.com:", "ssh://git@github.com/", "github.com/"} {
		if strings.HasPrefix(source, prefix) {
			normalized = "https://github.com/" + strings.TrimPrefix(source, prefix)
			break
		}
	}
	if u, err := url.Parse(normalized); err == nil && u.Host == "github.com" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 2 {
			u.Path = "/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
			ref := u.Query().Get("ref")
			if ref != "" || (subpath != "" && subpath != ".") {
				if ref == "" {
					ref = "HEAD"
				}
				u.Path += "/tree/" + ref
			}
			u.RawQuery = ""
		}
		if subpath != "" && subpath != "." {
			u = u.JoinPath(subpath)
		}
		return u.String()
	}
	if subpath == "" || subpath == "." {
		return source
	}
	return strings.TrimRight(source, "/\\") + "/" + filepath.ToSlash(subpath)
}

// home shortens a path under the user's home directory, which is where nearly
// every path in this interface lives.
func home(path string) string {
	dir, err := os.UserHomeDir()
	if err != nil || dir == "" || !strings.HasPrefix(path, dir+string(filepath.Separator)) {
		return path
	}
	return "~" + path[len(dir):]
}

// field keeps facts on one line, using the repository name for GitHub sources.
func field(t theme, label, value string, width int) []string {
	const labelWidth = 10
	style := t.Body
	if label == "From" {
		if u, err := url.Parse(value); err == nil && u.Host == "github.com" && (u.Scheme == "https" || u.Scheme == "http") {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) >= 2 {
				style = style.Hyperlink(value).Underline(true)
				value = parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
			}
		}
	}
	if width <= labelWidth+8 {
		return []string{t.Muted.Render(ansi.Truncate(label+" "+value, width, "…"))}
	}
	return []string{t.Muted.Render(pad(label, labelWidth)) +
		style.Render(elide(value, width-labelWidth))}
}

// elide shortens a value to one line. Paths keep their tail, which is the part
// that identifies them; anything else keeps its head.
func elide(value string, width int) string {
	if ansi.StringWidth(value) <= width || width < 4 {
		return ansi.Truncate(value, width, "…")
	}
	// Only filesystem paths are elided from the left: their tail identifies
	// them. A URL or a sentence is identified by its head.
	if !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "~") && !strings.HasPrefix(value, ".") {
		return ansi.Truncate(value, width, "…")
	}
	return "…" + ansi.TruncateLeft(value, ansi.StringWidth(value)-width+1, "")
}

// wrap renders text into at most n lines of the given width.
func wrap(style lipgloss.Style, text string, width, n int) []string {
	lines := strings.Split(style.Width(width).Render(text), "\n")
	if len(lines) > n {
		lines = append(lines[:n-1], style.Render(ansi.Truncate(ansi.Strip(lines[n-1]), max(1, width-1), "…")))
	}
	return lines
}

func (p *skillsPage) empty(a *app) string {
	if p.external {
		return "No skills found outside your library."
	}
	return "Nothing here yet. Press m, then Import skills."
}

// bindings names the space key for what it does here rather than for what it
// does in the lists that stage a selection.
func (p *skillsPage) bindings() []key.Binding {
	if p.focused {
		return []key.Binding{keys.Up, keys.Down, bind("enter", "run"), bind("esc", "back to list"), keys.Actions}
	}
	out := p.listPage.bindings()
	for i, b := range out {
		if b.Help().Key == "space" {
			out[i] = bind("space", "on/off", " ")
		}
	}
	if !p.external {
		out = append(out, bind("o", "on this machine"))
	}
	return out
}

// focus_ moves into the pane, if there is a skill to act on.
func (p *skillsPage) focus_(a *app) tea.Cmd {
	if p.skill() == nil {
		return nil
	}
	p.focused, p.action = true, 0
	return nil
}

// actions are the operations offered in the pane for the highlighted skill.
func (p *skillsPage) actions(a *app) []action {
	sk := p.skill()
	if sk == nil {
		return nil
	}
	actions := skillActions(a, *sk)
	if p.external {
		actions = append(actions, action{name: "Installation details…", desc: "Description and installed paths", run: func(a *app) tea.Cmd {
			body := library.Clean(sk.Description)
			for _, install := range sk.Installations {
				body += "\n\n" + install.Scope + " · " + library.Clean(install.Owner) + "\n" + library.Clean(install.Path)
			}
			return push(newDocPage(sk.Name, "On this machine", body))
		}})
	}
	return actions
}

// items are everything the pane's cursor can land on: first the harnesses,
// which space switches on and off for this repository, then the actions that
// apply to the highlighted skill.
func (p *skillsPage) items(a *app) []paneItem {
	var out []paneItem
	for _, agent := range p.agents(a) {
		out = append(out, paneItem{agent: agent})
	}
	for _, act := range p.actions(a) {
		out = append(out, paneItem{act: act})
	}
	if sk := p.skill(); sk != nil && p.upstreamFor == sk.Name {
		for _, c := range p.upstream {
			out = append(out, paneItem{cand: &c})
		}
	}
	return out
}

type paneItem struct {
	agent string             // a harness row, switched on and off for the repository
	act   action             // an action row, run with Enter
	cand  *library.Candidate // an expanded search result, recorded with Enter
}

// switchHarness links or unlinks the highlighted skill for one harness. This
// is the skill's own setting: the other skills in the repository keep theirs.
func (p *skillsPage) switchHarness(a *app, agent string) tea.Cmd {
	sk := p.skill()
	switch {
	case sk == nil:
		return nil
	case a.repo == "":
		return warn("Open a Git working tree to turn skills on.")
	case sk.HasScope("project"):
		return warn("%s is repository-owned; every harness here reads it from Git.", sk.Name)
	case sk.Entry == nil:
		return warn("%s is installed elsewhere. Skillverk does not link it.", sk.Name)
	}
	name := sk.Name
	var wanted []string
	for _, existing := range a.data.state.SkillAgents(name) {
		if existing != agent {
			wanted = append(wanted, existing)
		}
	}
	label := "Unlinking " + name + " from " + library.AgentLabel(agent)
	if len(wanted) == len(a.data.state.SkillAgents(name)) {
		wanted = append(wanted, agent)
		label = "Linking " + name + " into " + library.AgentLabel(agent)
	} else if len(wanted) == 0 {
		label = "Turning off " + name
	}
	return a.perform(label, func() ([]library.Result, error) {
		return a.store.SelectHarnesses(a.repo, name, wanted)
	})
}

// showsProgress reports that this screen draws the running job itself, so the
// footer leaves it alone.
func (p *skillsPage) showsProgress() bool { return p.working >= 0 }

func (p *skillsPage) update(a *app, msg tea.Msg) tea.Cmd {
	switch v := msg.(type) {
	case upstreamMsg:
		return p.found(a, v)
	case compareMsg:
		return p.compared(a, v)
	}
	if a.busy == "" {
		p.working = -1
	}
	if _, ok := msg.(reloadMsg); ok {
		// The list under the cursor has changed shape, and any search results
		// open under it describe a state that no longer holds.
		p.focused = false
		p.upstream, p.upstreamFor = nil, ""
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
		return push(newMenuPage(a))
	case "right", "l":
		return p.focus_(a)
	case "o":
		if p.external {
			return nil
		}
		if countExternal(a) == 0 {
			return warn("Every skill Skillverk can see is already in your library.")
		}
		return push(newExternalPage(a))
	}
	return p.listPage.update(a, msg)
}

// act handles the keys that belong to the pane while it holds the cursor.
func (p *skillsPage) act(a *app, k tea.KeyPressMsg) tea.Cmd {
	items := p.items(a)
	p.action = min(p.action, max(0, len(items)-1))
	switch k.String() {
	case "esc", "left", "h":
		p.focused = false
	case "up", "k":
		p.action = max(0, p.action-1)
	case "down", "j":
		p.action = min(len(items)-1, p.action+1)
	case "enter", " ", "space":
		if p.action >= len(items) {
			return nil
		}
		item := items[p.action]
		if item.cand != nil {
			return p.chooseUpstream(a, *item.cand)
		}
		// Remember the row before running it, so whatever it starts reports its
		// progress here rather than at the foot of the screen.
		p.working = p.action
		var cmd tea.Cmd
		if item.agent != "" {
			cmd = p.switchHarness(a, item.agent)
		} else {
			cmd = item.act.run(a)
		}
		if a.busy == "" {
			p.working = -1
		}
		return cmd
	case "m":
		return push(newMenuPage(a))
	}
	return nil
}

// ------------------------------------------------------- outside Skillverk

// newExternalPage lists the installations Skillverk did not make. They cannot
// be turned on, so the screen has no checkboxes: it is a list of things to
// inspect or adopt.
func newExternalPage(a *app) *skillsPage {
	p := newSkillsPage(a)
	p.external = true
	p.head = "On this machine"
	p.sub = "Skills from global folders, plugins, and parent folders."
	p.toggle = nil
	p.fill(a)
	return p
}

// countExternal reports how many installations Skillverk does not manage.
func countExternal(a *app) int {
	n := 0
	for _, sk := range a.data.skills {
		if !managed(sk) {
			n++
		}
	}
	return n
}
