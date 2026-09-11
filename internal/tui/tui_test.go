package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/CyberStefNef/skillverk/internal/library"
	"github.com/charmbracelet/x/ansi"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Space applies a change at once rather than staging it, and applies the
// reverse the same way.
func TestSpaceTurnsSkillOnAndOffImmediately(t *testing.T) {
	a, repo := fixture(t)
	focusOn(t, a, "zebra")
	press(t, a, "space")

	state, err := library.ReadState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Selected["zebra"] == "" {
		t.Fatal("space did not activate the skill")
	}
	if _, err := os.Lstat(filepath.Join(repo, ".claude", "skills", "zebra")); err != nil {
		t.Fatal("no native link created:", err)
	}
	focusOn(t, a, "zebra")
	p := a.top().(*skillsPage)
	if r, _ := p.focus(); r.check != checkOn {
		t.Fatal("the row did not follow the change:", r.check)
	}
	matrix := strings.Join(p.harnesses(a, *p.skill(), 60), "\n")
	for _, want := range []string{"Codex", "Claude Code", ".claude/skills/zebra"} {
		if !strings.Contains(matrix, want) {
			t.Fatal("harness matrix omits "+want+":", matrix)
		}
	}
	if state := a.top().(*skillsPage).state(a); state != "On here" {
		t.Fatal("detail pane does not say where it applies:", state)
	}

	press(t, a, "space")
	if state, err = library.ReadState(repo); err != nil || len(state.Selected) != 0 {
		t.Fatal(state, err)
	}
}

// Outside a Git working tree the library stays browsable, but nothing can be
// turned on, and the interface says so instead of failing quietly.
func TestOutsideGitTheLibraryStaysBrowsable(t *testing.T) {
	a, _ := fixture(t)
	outside, err := newApp(a.store, "")
	if err != nil {
		t.Fatal(err)
	}
	size(outside, 100, 32)
	focusOn(t, outside, "alpha")
	press(t, outside, "space")
	if outside.busy != "" {
		t.Fatal("activation attempted outside Git")
	}
	if !strings.Contains(outside.status.text, "Git working tree") {
		t.Fatal("no explanation given:", outside.status.text)
	}
	if listed := names(outside.top().(*skillsPage).listPage); len(listed) != 2 {
		t.Fatal("library not browsable outside Git:", listed)
	}
}

// Space on a harness line links or unlinks that one harness, leaving the
// repository's harness list and the skill's other links alone.
func TestPaneSwitchesOneHarness(t *testing.T) {
	a, repo := fixture(t)
	focusOn(t, a, "zebra")
	press(t, a, "space")
	press(t, a, "enter", "down", "space")

	state, err := library.ReadState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.SkillAgents("zebra"); len(got) != 1 || got[0] != "codex" {
		t.Fatal("space on a harness did not narrow the skill:", got)
	}
	if len(state.Agents) != 2 {
		t.Fatal("the repository's harnesses changed:", state.Agents)
	}
	if _, err := os.Lstat(filepath.Join(repo, ".claude", "skills", "zebra")); err == nil {
		t.Fatal("the unlinked harness still has a link")
	}
	if _, err := os.Lstat(filepath.Join(repo, ".agents", "skills", "zebra")); err != nil {
		t.Fatal("the other harness lost its link:", err)
	}
}

// Enter moves the cursor into the detail pane, where the skill's own actions
// are. It opens no new screen and changes nothing by itself.
func TestEnterMovesIntoThePaneWithoutChangingAnything(t *testing.T) {
	a, repo := fixture(t)
	focusOn(t, a, "alpha")
	press(t, a, "enter")

	p := a.top().(*skillsPage)
	if !p.focused {
		t.Fatal("Enter did not move into the pane")
	}
	if a.top().title() != "Skills" {
		t.Fatal("Enter stacked a screen:", a.top().title())
	}
	state, err := library.ReadState(repo)
	if err != nil || len(state.Selected) != 0 {
		t.Fatal("moving into the pane changed the selection", state, err)
	}
	var listed []string
	for _, act := range p.actions(a) {
		listed = append(listed, act.name)
	}
	for _, want := range []string{"Turn on here", "Update from its source", "Delete from library…"} {
		if !strings.Contains(strings.Join(listed, "|"), want) {
			t.Fatal("missing action", want, listed)
		}
	}
	if strings.Contains(strings.Join(listed, "|"), "Import skills") {
		t.Fatal("a skill's actions should not carry the menu:", listed)
	}
	press(t, a, "esc")
	if p.focused {
		t.Fatal("Esc did not return to the list")
	}
}

// The browser lists only the skills Skillverk can act on. Installations it
// does not manage live on their own screen, reached from the action palette.
func TestExternalInstallationsStayOutOfTheBrowser(t *testing.T) {
	a, _ := fixture(t)
	a.data.skills = append(a.data.skills, library.Skill{
		Name:          "global-writing",
		Description:   "House style for prose.",
		Installations: []library.Installation{{Scope: "global", Owner: "claude"}},
	})
	p := a.top().(*skillsPage)
	p.fill(a)
	if listed := names(p.listPage); len(listed) != 2 {
		t.Fatal("list should hold library skills only:", listed)
	}
	if view := ansi.Strip(a.View().Content); strings.Contains(view, "outside") {
		t.Fatal("external inventory should not appear on the main screen:", view)
	}
	found := false
	for _, r := range importPanel(a, newMenuPage(a)).rows {
		found = found || (r.label == "On this machine" && r.badge == "1 found")
	}
	if !found {
		t.Fatal("import does not offer the one installed skill")
	}
	press(t, a, "o")
	if a.top().title() != "On this machine" {
		t.Fatal("external inventory shortcut no longer works")
	}

	external := newExternalPage(a)
	listed := names(external.listPage)
	if len(listed) != 1 || listed[0] != "global-writing" {
		t.Fatal("external screen should list unmanaged installations only:", listed)
	}
	if external.toggle != nil {
		t.Fatal("unmanaged installations must not be toggleable")
	}
}

func TestMenuUsesSplitLayoutAndMTogglesIt(t *testing.T) {
	a, _ := fixture(t)
	press(t, a, "m")
	p, ok := a.top().(*menuPage)
	if !ok || p.title() != "Menu" {
		t.Fatal("m did not open the menu")
	}
	view := screen(a)
	if !strings.Contains(view, "│") || !strings.Contains(view, "On this machine") {
		t.Fatal("menu does not show an entry's own rows beside it:", view)
	}
	press(t, a, "down")
	index := p.list.Index()
	for _, code := range []rune{tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd} {
		_, cmd := a.Update(tea.KeyPressMsg{Code: code})
		deliver(t, a, cmd)
		if p.list.Index() != index {
			t.Fatal("removed navigation shortcut still moved the cursor")
		}
	}
	press(t, a, "m")
	if a.top().title() != "Skills" {
		t.Fatal("m did not close the menu")
	}
	press(t, a, "m", "/", "m")
	if a.top() == p || a.top().title() != "Menu" {
		t.Fatal("typing m in search closed the menu")
	}
}

// The machine skills open under their row in the menu, not on a screen of
// their own, and a skill its plugin owns says so rather than offering a move.
func TestImportExpandsMachineSkillsInThePane(t *testing.T) {
	a, _ := fixture(t)
	a.data.skills = append(a.data.skills, library.Skill{
		Name: "plugin-writing", Description: "Writing supplied by a plugin.",
		Installations: []library.Installation{{Scope: "plugin", Owner: "codex", Path: "/plugins/writing"}},
	})
	press(t, a, "m")
	focusOn(t, a, "Import skills")
	press(t, a, "enter")
	focusPaneRow(t, a, "On this machine")
	press(t, a, "enter")

	p, ok := a.top().(*menuPage)
	if !ok || len(a.stack) != 2 {
		t.Fatalf("expanding the machine skills left the menu: %T, %d deep", a.top(), len(a.stack))
	}
	if !strings.Contains(screen(a), "plugin-writing") {
		t.Fatal("the expanded section does not list the machine skill:", screen(a))
	}
	focusPaneRow(t, a, "plugin-writing")
	press(t, a, "enter")
	if len(a.stack) != 2 {
		t.Fatalf("a skill its plugin owns opened a screen: %T", a.top())
	}
	if !strings.Contains(a.status.text, "managed by its plugin") {
		t.Fatal("plugin ownership not explained:", a.status.text)
	}
	focusPaneRow(t, a, "On this machine")
	press(t, a, "enter")
	if p.expanded {
		t.Fatal("Enter did not close the section again")
	}
}

// The source is typed on its row, without a key to open a field first, and
// loading it replaces the menu with the skills it holds.
func TestSourceIsTypedInThePane(t *testing.T) {
	a, _ := fixture(t)
	source := writeSource(t, filepath.Join(t.TempDir(), "more"), "gamma")
	press(t, a, "m")
	focusOn(t, a, "Import skills")
	press(t, a, "enter")

	p := a.top().(*menuPage)
	if p.editing == nil {
		t.Fatal("the source field is not open where the cursor landed")
	}
	press(t, a, "down")
	if p.editing != nil {
		t.Fatal("the field stayed open after the cursor left its row")
	}
	press(t, a, "up")
	if p.editing == nil {
		t.Fatal("the field did not reopen when the cursor returned")
	}
	deliver(t, a, p.edit(a, tea.PasteMsg{Content: source}))
	press(t, a, "enter")
	if a.top().title() != "Choose skills" {
		t.Fatalf("loading a source did not open its skills: %s", a.top().title())
	}
	if len(a.stack) != 2 {
		t.Fatalf("importing is %d screens deep", len(a.stack))
	}
}

// An import ends on the question it raises: a skill in the library does
// nothing until this repository links it.
func TestImportAsksToTurnOnHere(t *testing.T) {
	a, repo := fixture(t)
	source := writeSource(t, filepath.Join(t.TempDir(), "more"), "gamma")
	press(t, a, "m")
	focusOn(t, a, "Import skills")
	press(t, a, "enter")
	p := a.top().(*menuPage)
	deliver(t, a, p.edit(a, tea.PasteMsg{Content: source}))
	press(t, a, "enter", "space", "enter")

	question, ok := a.top().(*confirmPage)
	if !ok {
		t.Fatalf("importing did not ask about this repository (%T)", a.top())
	}
	if question.choice != 1 {
		t.Fatal("a question that takes nothing away should default to yes")
	}
	if !strings.Contains(question.body, "gamma") {
		t.Fatal("the question does not name what was imported:", question.body)
	}
	press(t, a, "enter")

	state, err := library.ReadState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, on := state.Selected["gamma"]; !on {
		t.Fatal("answering yes did not turn the skill on here:", state.Selected)
	}
	if a.top().title() != "Skills" {
		t.Fatal("answering left the wrong screen:", a.top().title())
	}
}

// Search narrows the list, and Esc restores it without touching the library.
func TestSearchNarrowsAndEscClears(t *testing.T) {
	a, _ := fixture(t)
	press(t, a, "/", "z", "e", "b")
	p := a.top().(*skillsPage)
	if listed := names(p.listPage); len(listed) != 1 || listed[0] != "zebra" {
		t.Fatal("search did not narrow the list:", listed)
	}
	press(t, a, "enter")
	if !strings.Contains(a.top().lead(), "zeb") {
		t.Fatal("applied filter not shown in the header:", a.top().lead())
	}
	press(t, a, "esc")
	if len(names(p.listPage)) != 2 {
		t.Fatal("Esc did not clear the filter")
	}
}

// Importing copies into the library. It never turns anything on.
func TestImportSelectsWithoutActivating(t *testing.T) {
	a, repo := fixture(t)
	source := writeSource(t, filepath.Join(t.TempDir(), "more"), "gamma", "delta")
	c, err := library.OpenCollection(source)
	if err != nil {
		t.Fatal(err)
	}
	deliver(t, a, push(newSourcePage(a, c)))
	press(t, a, "a", "enter")

	found := map[string]bool{}
	for _, sk := range a.data.skills {
		found[sk.Name] = sk.Entry != nil
	}
	if !found["gamma"] || !found["delta"] {
		t.Fatal("import did not reach the library", found)
	}
	state, err := library.ReadState(repo)
	if err != nil || len(state.Selected) != 0 {
		t.Fatal("import activated skills", state, err)
	}
}

// Narrowing the source list must not discard selections made before it.
func TestImportSearchKeepsSelections(t *testing.T) {
	a, _ := fixture(t)
	source := writeSource(t, filepath.Join(t.TempDir(), "more"), "gamma", "delta")
	c, err := library.OpenCollection(source)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	deliver(t, a, push(newSourcePage(a, c)))
	focusOn(t, a, "delta")
	press(t, a, "space")
	press(t, a, "/", "g", "a", "m")
	press(t, a, "esc")

	p := a.top().(*listPage)
	selected := 0
	for _, r := range p.rows() {
		if r.check == checkOn {
			selected++
		}
	}
	if selected != 1 {
		t.Fatal("selection lost across the filter:", selected)
	}
}

// Entries the source could not parse stay reviewable; the valid ones remain
// importable alongside them.
func TestSkippedSourceEntriesStayReviewable(t *testing.T) {
	a, _ := fixture(t)
	dir := writeSource(t, filepath.Join(t.TempDir(), "mixed"), "gamma")
	broken := filepath.Join(dir, "broken")
	if err := os.MkdirAll(broken, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "SKILL.md"), []byte("no front matter here\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := library.OpenCollection(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if len(c.Skipped) == 0 {
		t.Skip("source parser accepted the malformed entry")
	}
	deliver(t, a, push(newSourcePage(a, c)))
	if listed := names(a.top().(*listPage)); len(listed) != 1 || listed[0] != "gamma" {
		t.Fatal("valid entries not importable:", listed)
	}
	press(t, a, "w")
	if a.top().title() != "Skipped entries" {
		t.Fatal("skipped entries not reviewable:", a.top().title())
	}
}

// A confirmation defaults to Cancel, so Enter alone never destroys anything.
func TestConfirmationDefaultsToCancel(t *testing.T) {
	a, _ := fixture(t)
	focusOn(t, a, "alpha")
	press(t, a, "enter")
	focusAction(t, a, "Delete from library…")
	press(t, a, "enter")

	confirm, ok := a.top().(*confirmPage)
	if !ok {
		t.Fatalf("no confirmation shown, got %T", a.top())
	}
	if confirm.choice != 0 {
		t.Fatal("confirmation preselected the destructive choice")
	}
	if !strings.Contains(confirm.body, "recorded managed links") {
		t.Fatal("effects not stated:", confirm.body)
	}
	press(t, a, "enter")
	if _, err := a.store.Path("alpha"); err != nil {
		t.Fatal("Enter on Cancel deleted the skill anyway")
	}
	if a.top().title() != "Skills" {
		t.Fatal("cancelling left the wrong screen:", a.top().title())
	}

	press(t, a, "esc")
	focusOn(t, a, "alpha")
	press(t, a, "enter")
	focusAction(t, a, "Delete from library…")
	press(t, a, "enter", "y")
	if _, err := a.store.Path("alpha"); err == nil {
		t.Fatal("confirmed deletion did not happen")
	}
}

// The menu is the last layer: a harness is switched in its pane, at once, and
// the menu is still the screen the user is on afterwards.
func TestHarnessSwitchesInTheMenuPane(t *testing.T) {
	a, repo := fixture(t)
	press(t, a, "m")
	focusOn(t, a, "Repository harnesses")
	press(t, a, "enter")
	if len(a.stack) != 2 {
		t.Fatalf("choosing a menu entry stacked a screen: %d deep", len(a.stack))
	}
	focusPaneRow(t, a, library.AgentLabel("opencode"))
	press(t, a, "enter")

	state, err := library.ReadState(repo)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, agent := range state.Agents {
		found = found || agent == "opencode"
	}
	if !found {
		t.Fatal("the pane did not apply the harness choice:", state.Agents)
	}
	if _, ok := a.top().(*menuPage); !ok || len(a.stack) != 2 {
		t.Fatalf("applying a harness left the menu: %T, %d deep", a.top(), len(a.stack))
	}

	press(t, a, "enter")
	if state, err = library.ReadState(repo); err != nil {
		t.Fatal(err)
	}
	for _, agent := range state.Agents {
		if agent == "opencode" {
			t.Fatal("pressing the row again did not switch the harness back off:", state.Agents)
		}
	}
}

// A value the menu holds is typed where it is shown, not on a screen of its
// own, and saves on Enter.
func TestReviewModelIsTypedInThePane(t *testing.T) {
	a, _ := fixture(t)
	settings := a.data.review
	settings.Provider = "claude"
	if err := a.store.SaveReviewSettings(settings); err != nil {
		t.Fatal(err)
	}
	a.data.review = settings
	press(t, a, "m")
	focusOn(t, a, "Setup review")
	press(t, a, "enter")
	focusPaneRow(t, a, "Model")
	press(t, a, "enter")

	p := a.top().(*menuPage)
	if p.editing == nil {
		t.Fatal("Enter on a value did not open its field")
	}
	if len(a.stack) != 2 {
		t.Fatalf("typing a value stacked a screen: %d deep", len(a.stack))
	}
	press(t, a, "o", "p", "u", "s", "enter")
	saved, err := a.store.ReviewSettings()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Models["claude"] != "opus" {
		t.Fatal("the field did not save what was typed:", saved.Models)
	}
	if p.editing != nil {
		t.Fatal("Enter left the field open")
	}
}

// Cancelling a load abandons it and ignores whatever it produces later.
func TestCancellingALoadIgnoresLateResults(t *testing.T) {
	a, _ := fixture(t)
	press(t, a, "m")
	focusOn(t, a, "Import skills")
	press(t, a, "enter")
	// Start a load without running it: the cancellation bookkeeping is what
	// this covers, not the fetch the source would perform.
	a.load("Loading a slow source", func(ctx context.Context, id uint64) tea.Msg {
		<-ctx.Done()
		return collectionMsg{id: id, err: ctx.Err()}
	})
	stale := a.jobID
	if a.busy == "" {
		t.Fatal("load did not report progress")
	}
	press(t, a, "esc")
	if a.busy != "" {
		t.Fatal("Esc did not cancel the load")
	}
	c, err := library.OpenCollection(writeSource(t, filepath.Join(t.TempDir(), "late"), "gamma"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	deliver(t, a, func() tea.Msg { return collectionMsg{id: stale, c: c} })
	if a.top().title() == "Choose skills" {
		t.Fatal("a cancelled load still opened its source")
	}
}

// Opening the interface reports pending state without silently retrying it.
func TestOpeningDoesNotRetryPartialState(t *testing.T) {
	a, repo := fixture(t)
	focusOn(t, a, "alpha")
	press(t, a, "space")
	link := filepath.Join(repo, ".claude", "skills", "alpha")
	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	reopened, err := newApp(a.store, repo)
	if err != nil {
		t.Fatal(err)
	}
	size(reopened, 100, 32)
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("opening the interface re-applied the link")
	}
}

// Every screen fits the terminal it is given, at both a comfortable and a
// cramped size, and always keeps its footer controls.
func TestEveryScreenFitsTheTerminal(t *testing.T) {
	a, _ := fixture(t)
	source := writeSource(t, filepath.Join(t.TempDir(), "more"), "gamma")
	c, err := library.OpenCollection(source)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	screens := []struct {
		name string
		page page
	}{
		{"menu", newMenuPage(a)},
		{"actions", newActionsPage(a, &a.data.skills[0])},
		{"menu import", menuPane(a, "Import skills")},
		{"source", newSourcePage(a, c)},
		{"on this machine", newExternalPage(a)},
		{"menu pane", menuPane(a, "Repository harnesses")},
		{"settings", newSettingsPage(a)},
		{"originals", newOriginalsPage(a, "")},
		{"adoption", newAdoptionOfferPage(a)},
		{"setup", newSetupOfferPage(nil)},
		{"details", newDocPage("Last result", "", strings.Repeat("a result line\n", 80))},
		{"confirm", newConfirmPage("Delete", "Delete", strings.Repeat("effect line\n", 80), nil)},
	}
	for _, dimensions := range [][2]int{{120, 40}, {80, 24}, {60, 14}, {40, 10}} {
		width, height := dimensions[0], dimensions[1]
		for _, s := range screens {
			a.stack = []page{a.stack[0], s.page}
			size(a, width, height)
			lines := strings.Split(screen(a), "\n")
			if len(lines) > height {
				t.Fatalf("%s at %dx%d rendered %d lines", s.name, width, height, len(lines))
			}
			for i, line := range lines {
				if w := ansi.StringWidth(line); w > width {
					t.Fatalf("%s at %dx%d line %d is %d cells wide", s.name, width, height, i, w)
				}
			}
			if footer := lines[len(lines)-1]; !strings.Contains(footer, "quit") {
				t.Fatalf("%s at %dx%d lost its footer controls: %q", s.name, width, height, footer)
			}
		}
	}
}

// The README image is a real frame, not a mock-up. Regenerate it with
// `make preview` after any change to the interface.
func TestRenderDocumentation(t *testing.T) {
	a, _ := fixture(t)
	focusOn(t, a, "zebra")
	press(t, a, "space")
	size(a, 112, 32)

	a.repo = "/work/my-repository"
	a.status = noteMsg{}
	for i := range a.data.skills {
		sk := &a.data.skills[i]
		switch sk.Name {
		case "zebra":
			sk.Name = "grill-with-docs"
			sk.Description = "A relentless interview to sharpen a plan or design, which also creates docs (ADR's and glossary) as we go."
		case "alpha":
			sk.Name = "tdd"
			sk.Description = "Build features and fix bugs with test-driven development."
		}
		sk.Path = "/private/skillverk/content/" + sk.Name
		if sk.Entry != nil {
			sk.Entry.Source = "https://github.com/mattpocock/skills.git"
			sk.Entry.Subpath = "skills/engineering/" + sk.Name
		}
	}
	a.data.skills = append(a.data.skills, library.Skill{
		Name:          "global-writing",
		Description:   "House style for prose: plain words, short sentences, no filler.",
		Installations: []library.Installation{{Scope: "global", Owner: "claude"}},
	})
	page := a.top().(*skillsPage)
	page.fill(a)
	page.list.Select(0)

	view := a.View().Content
	for _, want := range []string{"grill-with-docs", "tdd", "mattpocock/skills", "quit"} {
		if !strings.Contains(ansi.Strip(view), want) {
			t.Fatal("documentation frame is missing", want)
		}
	}
	if path := os.Getenv("SKILLVERK_RENDER_PATH"); path != "" {
		if err := os.WriteFile(path, []byte(view), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// An adopted skill still says where it was adopted from. Skillverk keeps the
// original in custody beside its old home, so the custody path names that home
// even once the original has been deleted.
func TestAdoptedSkillsNameWhereTheyCameFrom(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{"/home/s/work/pdx-cif/.git/skillverk-originals/b45c/original", "/home/s/work/pdx-cif"},
		{"/home/s/.codex/skills/.skillverk-original-1684994825/original", "/home/s/.codex/skills"},
		// Import staging is Skillverk's own temporary, not a place to point at.
		{"/home/s/.local/share/skillverk/.import-99/original", ""},
		{"https://github.com/mattpocock/skills.git", ""},
		{"/home/s/plain/folder", ""},
	} {
		if got := adoptedFrom(c.source); got != c.want {
			t.Errorf("adoptedFrom(%q) = %q, want %q", c.source, got, c.want)
		}
	}
	from := origin(library.Entry{Source: "/home/s/work/pdx-cif/.git/skillverk-originals/b45c/original", Subpath: "."})
	if !strings.Contains(from, "pdx-cif") || !strings.Contains(from, "adopted") {
		t.Fatal("the From line hides where the skill was adopted from:", from)
	}
}

// An adopted skill has no source to fetch. Saying so is the whole job: the old
// message named a .git custody directory the user never created.
func TestAdoptedSkillsExplainWhyTheyCannotBeChecked(t *testing.T) {
	adopted := library.Entry{Source: "/home/s/work/pdx-cif/.git/skillverk-originals/b45c/original", Subpath: "."}
	short, detail := sourceProblem(adopted)
	if !strings.Contains(short, "adopted") || !strings.Contains(short, "no source") {
		t.Fatal("the clause does not say why:", short)
	}
	if strings.Contains(short, "skillverk-originals") || strings.Contains(short, ".git") {
		t.Fatal("the status line still shows an internal path:", short)
	}
	if !strings.Contains(detail, "/home/s/work/pdx-cif") || !strings.Contains(detail, "--replace") {
		t.Fatal("the detail neither locates the skill nor says what to do:", detail)
	}

	// A source that was real and has gone is a different sentence.
	short, detail = sourceProblem(library.Entry{Source: "/definitely/not/here", Subpath: "."})
	if !strings.Contains(short, "no longer has a source") || !strings.Contains(detail, "library copy is safe") {
		t.Fatal("a vanished import source is not explained:", short, detail)
	}

	// Sources worth trying are left alone.
	for _, e := range []library.Entry{
		{Upstream: "https://github.com/mattpocock/skills.git", UpstreamPath: "skills/engineering/grill-with-docs"},
		{Source: "https://github.com/mattpocock/skills.git", Subpath: "skills/productivity/wizard"},
	} {
		if short, _ := sourceProblem(e); short != "" {
			t.Fatal("a reachable source was refused:", short)
		}
	}

}

// One row about the source, and it is the row that can do something. A skill
// with no source is offered the search; one with a source is offered the check.
// Offering both was two dead ends on every adopted skill.
func TestOneSourceActionAtATime(t *testing.T) {
	a, _ := fixture(t)
	named := func(sk library.Skill) []string {
		var out []string
		for _, act := range skillActions(a, sk) {
			out = append(out, act.name)
		}
		return out
	}
	adopted := named(library.Skill{Name: "alpha", Entry: &library.Entry{
		Source: "/home/s/work/pdx-cif/.git/skillverk-originals/b45c/original", Subpath: "."}})
	if !slices.Contains(adopted, "Find its source…") {
		t.Fatal("an adopted skill is not offered the search:", adopted)
	}
	for _, gone := range []string{"Update from its source…", "Check for updates"} {
		if slices.Contains(adopted, gone) {
			t.Fatal("an adopted skill is still offered "+gone+":", adopted)
		}
	}
	followed := named(library.Skill{Name: "alpha", Entry: &library.Entry{
		Upstream: "https://github.com/mattpocock/skills.git", UpstreamPath: "skills/alpha"}})
	if !slices.Contains(followed, "Update from its source…") {
		t.Fatal("a skill with a source cannot check it:", followed)
	}
	if slices.Contains(followed, "Find its source…") {
		t.Fatal("a skill with a source is asked to find one:", followed)
	}
	if len(followed) > 5 {
		t.Fatalf("the action list grew back to %d rows: %v", len(followed), followed)
	}
}

// The three drift verdicts want three different next steps, which is the whole
// reason the baseline is recorded. Each must say which side moved and what that
// means for the user's own changes.
func TestDriftReportsSayWhichSideMoved(t *testing.T) {
	for _, c := range []struct {
		drift     library.Drift
		says      string
		detail    string
		forbidden string
	}{
		{library.Drift{Baseline: true, Moved: true}, "out of date", "loses nothing of yours", "discard"},
		{library.Drift{Baseline: true, Edited: true}, "was edited here", "changes are", "out of date"},
		{library.Drift{Baseline: true, Moved: true, Edited: true}, "diverged", "would lose your edits", ""},
		{library.Drift{}, "differs from its source", "not recorded", "out of date"},
	} {
		text, detail := driftReport("alpha", c.drift)
		if !strings.Contains(text, c.says) {
			t.Errorf("%+v reported as %q, want it to say %q", c.drift, text, c.says)
		}
		if !strings.Contains(detail, c.detail) {
			t.Errorf("%+v detail omits %q: %s", c.drift, c.detail, detail)
		}
		if c.forbidden != "" && strings.Contains(text+detail, c.forbidden) {
			t.Errorf("%+v wrongly mentions %q", c.drift, c.forbidden)
		}
	}
}

// A candidate says what it proves. An identical copy settles the question; a
// name match must not be dressed up as one.
func TestUpstreamCandidatesStateTheirEvidence(t *testing.T) {
	certain, badge, tone := proves(library.Candidate{Identical: true})
	if badge != "certain" || tone != levelDone || !strings.Contains(certain, "same as your copy") {
		t.Fatal("an identical match is not presented as settled:", certain, badge)
	}
	guess, badge, tone := proves(library.Candidate{})
	if badge != "unconfirmed" || tone != levelWarn || !strings.Contains(guess, "different skill") {
		t.Fatal("a name-only match is not presented as a guess:", guess, badge)
	}
}

// A search answers where it was asked. The results open under the actions, on
// the same screen, and the cursor lands on the best match.
func TestUpstreamResultsExpandInThePane(t *testing.T) {
	a, _ := fixture(t)
	focusOn(t, a, "alpha")
	press(t, a, "enter")
	p := a.top().(*skillsPage)
	before := len(p.items(a))

	found := []library.Candidate{
		{Upstream: "https://github.com/mattpocock/skills.git", UpstreamPath: "skills/engineering/alpha", Name: "alpha", Identical: true},
		{Upstream: "https://github.com/vercel-labs/skills.git", UpstreamPath: "skills/alpha", Name: "alpha"},
	}
	deliver(t, a, func() tea.Msg { return upstreamMsg{id: a.jobID, name: "alpha", candidates: found} })

	if a.top().title() != "Skills" {
		t.Fatal("the search opened a screen instead of expanding:", a.top().title())
	}
	items := p.items(a)
	if len(items) != before+2 {
		t.Fatalf("candidates did not expand into the pane: %d rows, was %d", len(items), before)
	}
	if items[len(items)-2].cand == nil || !items[len(items)-2].cand.Identical {
		t.Fatal("the best match is not the first candidate row")
	}
	if p.action != before {
		t.Fatalf("the cursor did not land on the first candidate: %d, want %d", p.action, before)
	}
	pane := strings.Join(p.detail(a, 70, 40), "\n")
	for _, want := range []string{"mattpocock/skills", "certain", "unconfirmed"} {
		if !strings.Contains(pane, want) {
			t.Fatal("the pane omits "+want+":", pane)
		}
	}

	// Enter on a candidate records it without leaving the screen.
	press(t, a, "enter")
	if a.top().title() != "Skills" {
		t.Fatal("recording an upstream left the skills screen:", a.top().title())
	}
	entry := ""
	for _, sk := range a.data.skills {
		if sk.Name == "alpha" && sk.Entry != nil {
			entry = sk.Entry.Upstream
		}
	}
	if entry != "https://github.com/mattpocock/skills.git" {
		t.Fatal("the chosen upstream was not recorded:", entry)
	}
}

// Work started from a pane row reports itself on that row, not at the foot of
// the terminal where the key was never pressed.
func TestPaneWorkShowsItsProgressLocally(t *testing.T) {
	a, _ := fixture(t)
	focusOn(t, a, "alpha")
	press(t, a, "enter")
	p := a.top().(*skillsPage)
	p.working = len(p.agents(a))
	a.busy = "Searching your sources for alpha"

	if !p.showsProgress() {
		t.Fatal("the pane does not claim the running job")
	}
	if line := a.statusLine(80); line != "" {
		t.Fatal("the footer also reported the job:", line)
	}
	pane := strings.Join(p.detail(a, 70, 40), "\n")
	if !strings.Contains(pane, "Searching your sources") || !strings.Contains(pane, "esc cancel") {
		t.Fatal("the pane does not show the running job on its row:", pane)
	}

	// When nothing is running the footer takes its line back.
	a.busy, p.working = "", -1
	if p.showsProgress() {
		t.Fatal("an idle pane still claims the footer")
	}
}

// What destroys something says so, in the word the confirmation uses. A row
// reading "remove" for an action that deletes content out of every repository
// hides the weight of the choice until the next screen.
func TestDestructiveActionsNameTheAct(t *testing.T) {
	a, _ := fixture(t)
	sk := library.Skill{Name: "alpha", Entry: &library.Entry{
		Upstream: "https://github.com/mattpocock/skills.git", UpstreamPath: "skills/alpha"}}
	var deleting action
	for _, act := range skillActions(a, sk) {
		if strings.Contains(act.name, "library…") && act.danger {
			deleting = act
		}
		if !act.danger && strings.Contains(strings.ToLower(act.name), "delete") {
			t.Fatal("a deleting action is not marked dangerous:", act.name)
		}
	}
	if deleting.name != "Delete from library…" {
		t.Fatal("the library deletion is not named as a deletion:", deleting.name)
	}
	if strings.Contains(strings.ToLower(deleting.name), "remove") {
		t.Fatal("the row still softens the act:", deleting.name)
	}

	// The row is coloured as a warning while it is not under the cursor, so the
	// list reads as dangerous before anything is pressed.
	p := a.top().(*skillsPage)
	p.focused = true
	plain := p.paneRow(a, deleting.name, 99, 40, false)
	danger := p.paneRow(a, deleting.name, 99, 40, true)
	if plain == danger {
		t.Fatal("a destructive row is drawn exactly like a harmless one")
	}

	// The palette says it too, for the same actions.
	rows := actionRows([]action{{name: "Delete from library…", danger: true}, {name: "Turn on here"}})
	if rows[0].badge != "deletes" || rows[0].tone != levelWarn {
		t.Fatal("the palette does not mark the destructive row:", rows[0])
	}
	if rows[1].badge != "" {
		t.Fatal("a harmless action was badged:", rows[1])
	}
}

func TestSourceLocationsAreCompletePaths(t *testing.T) {
	for _, c := range []struct{ source, subpath, want string }{
		{"https://github.com/mattpocock/skills.git", "skills/engineering/grill-with-docs", "https://github.com/mattpocock/skills/tree/HEAD/skills/engineering/grill-with-docs"},
		{"git@github.com:owner/repo.git", "review", "https://github.com/owner/repo/tree/HEAD/review"},
		{"https://github.com/owner/repo.git?ref=stable", "review", "https://github.com/owner/repo/tree/stable/review"},
		{"https://github.com/owner/repo.git?ref=stable", ".", "https://github.com/owner/repo/tree/stable"},
		{"https://github.com/owner/repo/tree/main/skills", "review", "https://github.com/owner/repo/tree/main/skills/review"},
		{"https://github.com/owner/repo.git", ".", "https://github.com/owner/repo"},
		{"/work/skills", "review", "/work/skills/review"},
	} {
		if got := origin(library.Entry{Source: c.source, Subpath: c.subpath}); got != c.want {
			t.Errorf("origin(%q, %q) = %q, want %q", c.source, c.subpath, got, c.want)
		}
	}
}

func TestFromShowsRepositoryName(t *testing.T) {
	target := "https://github.com/mattpocock/skills/tree/HEAD/skills/engineering/grill-with-docs"
	lines := field(newTheme(true), "From", target, 45)
	if !strings.Contains(lines[0], ansi.SetHyperlink(target)) || !strings.Contains(lines[0], ansi.ResetHyperlink()) {
		t.Fatalf("source label is missing its hyperlink: %q", lines)
	}
	if len(lines) != 1 || strings.TrimSpace(strings.TrimPrefix(ansi.Strip(lines[0]), "From")) != "mattpocock/skills" {
		t.Fatalf("expected a short repository label, got %q", lines)
	}
}

func TestSelectedHarnessKeepsStatusColor(t *testing.T) {
	a, _ := fixture(t)
	focusOn(t, a, "alpha")
	press(t, a, "space", "enter")
	p := a.top().(*skillsPage)
	lines := p.harnesses(a, *p.skill(), 60)
	// The selected Codex row must retain the same green as the Claude row.
	green := strings.TrimSuffix(a.theme.Good.Render("●"), "\x1b[m")
	if !strings.Contains(lines[1], green) || !strings.Contains(lines[2], green) {
		t.Fatalf("harness status lost its green color: %q", lines)
	}
}

func TestSourceCanBeOpenedFromActions(t *testing.T) {
	a, _ := fixture(t)
	sk := library.Skill{Name: "review", Entry: &library.Entry{
		Source: "https://github.com/mattpocock/skills.git", Subpath: "skills/engineering/grill-with-docs",
	}}
	found := false
	for _, action := range skillActions(a, sk) {
		if action.name == "Open source in browser" {
			found = action.run != nil
		}
	}
	if !found {
		t.Fatal("missing source browser action")
	}
	for platform, command := range map[string]string{"linux": "xdg-open", "darwin": "open", "windows": "rundll32"} {
		target := origin(*sk.Entry)
		args := browserArgs(platform, target)
		if args[0] != command || args[len(args)-1] != target {
			t.Fatalf("invalid browser arguments for %s: %q", platform, args)
		}
	}
}
