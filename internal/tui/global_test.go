package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CyberStefNef/skillverk/internal/library"
)

func openGlobalPicker(t *testing.T, a *app) {
	t.Helper()
	press(t, a, "m")
	focusOn(t, a, "Global skills")
	press(t, a, "enter")
}

func TestGlobalPickerKeepsRepositorySelectionIndependent(t *testing.T) {
	a, repo := fixture(t)
	size(a, 112, 32)
	focusOn(t, a, "zebra")
	press(t, a, "space")
	statePath := filepath.Join(repo, ".git", "skillverk-state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	press(t, a, "g")
	p := a.top().(*skillsPage)
	if a.global || p.skill().Name != "zebra" || p.state(a) != "On here" {
		t.Fatal("g changed the displayed scope or repository selection:", screen(a))
	}
	for _, want := range []string{"Global", "global on/off", "Codex · Claude Code"} {
		if !strings.Contains(screen(a), want) {
			t.Fatal("global screen omits", want, screen(a))
		}
	}
	if r, _ := p.focus(); r.badge != "global" {
		t.Fatal("repository list does not mark globally enabled skills")
	}
	for _, agent := range library.DefaultAgents {
		root, err := library.GlobalRoot(agent)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, "zebra", "SKILL.md")); err != nil {
			t.Fatal("global link missing:", err)
		}
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("global activation changed repository state", err)
	}
	press(t, a, "g")
	if a.global || a.top().(*skillsPage).state(a) != "On here" {
		t.Fatal("global deactivation changed repository selection:", screen(a))
	}
	for _, agent := range library.DefaultAgents {
		if _, err := os.Stat(filepath.Join(repo, library.AgentPath(agent), "zebra", "SKILL.md")); err != nil {
			t.Fatal("repository link removed:", err)
		}
	}
	sk, err := a.store.Find(repo, "zebra")
	if err != nil || len(sk.GlobalStates) != 0 {
		t.Fatal("second g did not remove global links", sk, err)
	}
	press(t, a, "g", "space")
	sk, err = a.store.Find(repo, "zebra")
	if err != nil || sk.Selected || sk.GlobalStates["codex"] != "active" {
		t.Fatal("Space removed the global links instead of the repository selection", sk, err)
	}
	openGlobalPicker(t, a)
	focusOn(t, a, "zebra")
	p = a.top().(*skillsPage)
	if p.state(a) != "On globally" || names(p.listPage)[0] != "zebra" {
		t.Fatal("global activation did not appear in the global view:", screen(a))
	}
	press(t, a, "g")
	if !a.global || p.state(a) != "Off globally" {
		t.Fatal("g did not toggle globally in the global view")
	}
}

func TestGlobalPickerChangesIndividualHarnesses(t *testing.T) {
	a, repo := fixture(t)
	openGlobalPicker(t, a)
	focusOn(t, a, "alpha")
	press(t, a, "space", "enter", "down", "space")
	sk, err := a.store.Find("", "alpha")
	if err != nil || sk.GlobalStates["codex"] != "active" || sk.GlobalStates["claude"] != "" {
		t.Fatal("individual global switch changed other harnesses", sk, err)
	}
	press(t, a, "enter")
	focusAction(t, a, "Show all harnesses")
	press(t, a, "enter")
	p := a.top().(*skillsPage)
	if !slices.Contains(p.agents(a), "opencode") {
		t.Fatal("cannot select another global client")
	}
	for i, item := range p.items(a) {
		if item.agent == "opencode" {
			p.action = i
			break
		}
	}
	press(t, a, "space")
	sk, err = a.store.Find("", "alpha")
	if err != nil || sk.GlobalStates["opencode"] != "active" || sk.GlobalStates["codex"] != "active" {
		t.Fatal("another harness did not get its global link", sk, err)
	}
	state, err := library.ReadState(repo)
	if err != nil || len(state.Selected) != 0 {
		t.Fatal("global harness changes touched repository selection", state, err)
	}
	press(t, a, "enter")
	size(a, 60, 20)
	focusAction(t, a, "Turn off globally")
	if !strings.Contains(screen(a), "› Turn off globally") {
		t.Fatal("focused action is hidden on a small terminal:", screen(a))
	}
	press(t, a, "enter")
	sk, err = a.store.Find("", "alpha")
	if err != nil || len(sk.GlobalStates) != 0 {
		t.Fatal("global off left a recorded harness", sk, err)
	}
}

func TestGlobalPickerPreservesConflictsAndOffersRetry(t *testing.T) {
	a, _ := fixture(t)
	root, err := library.GlobalRoot("claude")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "alpha")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("unmanaged installation"), 0644); err != nil {
		t.Fatal(err)
	}
	openGlobalPicker(t, a)
	focusOn(t, a, "alpha")
	press(t, a, "space")
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "unmanaged installation" {
		t.Fatal("global picker replaced an unmanaged installation", err)
	}
	if a.status.lvl != levelError || !strings.Contains(screen(a), "needs attention") {
		t.Fatal("conflict was not reported:", screen(a))
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	press(t, a, "m")
	if slices.Contains(names(a.top().(*menuPage).listPage), "Repository harnesses") {
		t.Fatal("global menu offers repository changes")
	}
	focusOn(t, a, "Retry global changes")
	press(t, a, "enter")
	sk, err := a.store.Find("", "alpha")
	if err != nil || sk.GlobalStates["claude"] != "active" {
		t.Fatal("retry did not repair the global link", sk, err)
	}
}

func TestGlobalShortcutRespectsSearchAndBusyState(t *testing.T) {
	a, _ := fixture(t)
	press(t, a, "/", "g")
	if a.global || a.top().(*skillsPage).list.FilterValue() != "g" {
		t.Fatal("typing g into search changed scope")
	}
	sk, err := a.store.Find("", "alpha")
	if err != nil || len(sk.GlobalStates) > 0 {
		t.Fatal("typing g activated a skill", sk, err)
	}
	press(t, a, "esc")
	a.busy = "Changing links"
	press(t, a, "g")
	sk, err = a.store.Find("", "alpha")
	if err != nil || len(sk.GlobalStates) > 0 {
		t.Fatal("g activated a skill during an operation", sk, err)
	}
	a.busy = ""
	press(t, a, "m")
	focusOn(t, a, "Global skills")
	press(t, a, "enter")
	if !a.global || len(a.stack) != 1 {
		t.Fatal("menu did not switch to the global picker")
	}
	press(t, a, "enter", "g")
	if !a.global || !a.selected(*a.top().(*skillsPage).skill()) {
		t.Fatal("g in the pane did not toggle the highlighted skill globally")
	}
	press(t, a, "m")
	focusOn(t, a, "Repository skills")
	press(t, a, "enter")
	if a.global {
		t.Fatal("menu did not return to repository skills")
	}
}

func TestGlobalImportOffersGlobalActivation(t *testing.T) {
	a, repo := fixture(t)
	openGlobalPicker(t, a)
	source := writeSource(t, filepath.Join(tempDir(t), "source"), "gamma")
	c, err := library.OpenCollection(source)
	if err != nil {
		t.Fatal(err)
	}
	deliver(t, a, push(newSourcePage(a, c)))
	press(t, a, "space", "enter")
	if a.top().title() != "Turn them on globally?" {
		t.Fatal("global import offered the wrong scope:", screen(a))
	}
	press(t, a, "enter")
	sk, err := a.store.Find("", "gamma")
	if err != nil || sk.GlobalStates["codex"] != "active" || sk.GlobalStates["claude"] != "active" {
		t.Fatal("import did not activate globally", sk, err)
	}
	state, err := library.ReadState(repo)
	if err != nil || len(state.Selected) != 0 {
		t.Fatal("global import activated repository links", state, err)
	}
}
