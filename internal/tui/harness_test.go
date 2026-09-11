package tui

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/CyberStefNef/skillverk/internal/library"
	"github.com/charmbracelet/x/ansi"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixture builds a library with two importable skills and an empty Git
// working tree, then opens the interface on it.
func fixture(t *testing.T) (*app, string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repository")
	if err := os.Mkdir(repo, 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	store, err := library.New(filepath.Join(root, "library"))
	if err != nil {
		t.Fatal(err)
	}
	store.ScanRoots = []library.ScanRoot{}
	source := writeSource(t, filepath.Join(root, "source"), "alpha", "zebra")
	c, err := library.OpenCollection(source)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = store.Publish(c, []string{"alpha", "zebra"}, false); err != nil {
		t.Fatal(err)
	}
	a, err := newApp(store, repo)
	if err != nil {
		t.Fatal(err)
	}
	size(a, 100, 32)
	return a, repo
}

// writeSource creates a directory of importable skills.
func writeSource(t *testing.T, dir string, names ...string) string {
	t.Helper()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: Test skill " + name + "\n---\nInert fixture.\n"
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func size(a *app, width, height int) {
	a.Update(tea.WindowSizeMsg{Width: width, Height: height})
}

// keyOf turns a key name into the press the program would receive.
func keyOf(name string) tea.KeyPressMsg {
	switch name {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+k":
		return tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}
	default:
		r := []rune(name)[0]
		return tea.KeyPressMsg{Code: r, Text: string(r)}
	}
}

// press sends one key and follows every message it produces.
func press(t *testing.T, a *app, names ...string) {
	t.Helper()
	for _, name := range names {
		_, cmd := a.Update(keyOf(name))
		deliver(t, a, cmd)
	}
}

// deliver applies each message a command produces, unwrapping the composite
// commands Bubble Tea builds for Batch and Sequence. The depth limit stops
// self-renewing animations — a blinking cursor never settles.
func deliver(t *testing.T, a *app, cmd tea.Cmd) { follow(t, a, cmd, 0) }

func follow(t *testing.T, a *app, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth > 32 {
		return
	}
	msg := cmd()
	if msg == nil || animation(msg) {
		return
	}
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
		for i := 0; i < v.Len(); i++ {
			follow(t, a, v.Index(i).Interface().(tea.Cmd), depth+1)
		}
		return
	}
	_, next := a.Update(msg)
	if _, note := msg.(noteMsg); note {
		return // its expiry timer would block the test for seconds
	}
	follow(t, a, next, depth+1)
}

// animation reports the periodic messages that keep redrawing themselves.
func animation(msg tea.Msg) bool {
	if _, ok := msg.(spinner.TickMsg); ok {
		return true
	}
	if _, ok := msg.(tea.QuitMsg); ok {
		return true
	}
	return strings.Contains(strings.ToLower(reflect.TypeOf(msg).String()), "blink")
}

// focusOn moves the cursor to a named row on the visible list.
func focusOn(t *testing.T, a *app, name string) {
	t.Helper()
	p, ok := listOf(a.top())
	if !ok {
		t.Fatalf("top page %T is not a list", a.top())
	}
	for i, item := range p.list.VisibleItems() {
		if item.(row).name == name {
			p.list.Select(i)
			return
		}
	}
	t.Fatalf("row %q not listed; have %s", name, strings.Join(names(p), ", "))
}

// focusAction moves the cursor to a named action in the skills pane.
func focusAction(t *testing.T, a *app, name string) {
	t.Helper()
	p, ok := a.top().(*skillsPage)
	if !ok || !p.focused {
		t.Fatalf("the pane does not hold the cursor (%T)", a.top())
	}
	var listed []string
	for i, item := range p.items(a) {
		if item.agent != "" {
			continue
		}
		if item.act.name == name {
			p.action = i
			return
		}
		listed = append(listed, item.act.name)
	}
	t.Fatalf("action %q not offered; have %s", name, strings.Join(listed, ", "))
}

// focusPaneRow moves the pane's cursor to a named row.
func focusPaneRow(t *testing.T, a *app, label string) {
	t.Helper()
	p, ok := a.top().(*menuPage)
	if !ok || !p.focused {
		t.Fatalf("the pane does not hold the cursor (%T)", a.top())
	}
	var listed []string
	for i, r := range p.panelRows(a) {
		if r.label == label {
			p.cursor = i
			p.sync(a)
			return
		}
		listed = append(listed, r.label)
	}
	t.Fatalf("pane row %q not offered; have %s", label, strings.Join(listed, ", "))
}

// menuPane opens the menu with one entry's pane holding the cursor.
func menuPane(a *app, entry string) *menuPage {
	p := newMenuPage(a)
	for i, item := range p.list.VisibleItems() {
		if item.(row).name == entry {
			p.list.Select(i)
			break
		}
	}
	p.focused = true
	return p
}

func listOf(p page) (*listPage, bool) {
	switch v := p.(type) {
	case *listPage:
		return v, true
	case *skillsPage:
		return v.listPage, true
	case *menuPage:
		return v.listPage, true
	case *setupOfferPage:
		return v.listPage, v.listPage != nil
	}
	return nil, false
}

func names(p *listPage) []string {
	var out []string
	for _, item := range p.list.VisibleItems() {
		out = append(out, item.(row).name)
	}
	return out
}

// screen renders the current frame as plain text.
func screen(a *app) string { return ansi.Strip(a.View().Content) }
