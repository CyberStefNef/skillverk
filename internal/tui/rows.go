package tui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/CyberStefNef/skillverk/internal/library"
	"github.com/charmbracelet/x/ansi"
	"io"
	"strings"
)

// check states a row can be in. Rows that are not selectable use checkNone.
const (
	checkNone = iota
	checkOff
	checkOn
)

// row is the single item type every list in the interface uses. Screens put
// their own value in data and read it back from the focused row.
type row struct {
	id    string
	name  string
	desc  string
	badge string
	tone  level
	check int
	data  any
}

func (r row) FilterValue() string { return r.name + " " + r.desc }

func items(rows []row) []list.Item {
	out := make([]list.Item, len(rows))
	for i, r := range rows {
		out[i] = r
	}
	return out
}

// rowDelegate draws one row as an optional checkbox, a name, a right-aligned
// state badge, and, when the screen asks for it, a description line.
type rowDelegate struct {
	theme    theme
	showDesc bool
	// blurred draws the list without its cursor, so a split screen shows one
	// selection at a time.
	blurred bool
}

func (d rowDelegate) Height() int {
	if d.showDesc {
		return 2
	}
	return 1
}
func (d rowDelegate) Spacing() int                        { return 0 }
func (d rowDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d rowDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	r, ok := item.(row)
	if !ok {
		return
	}
	t := d.theme
	width := max(12, m.Width()-2)
	focused := index == m.Index() && !d.blurred

	// One base style carries the selection background so every segment below
	// keeps it while re-tinting its foreground.
	base := lipgloss.NewStyle().Foreground(t.muted)
	if focused {
		base = lipgloss.NewStyle().Foreground(t.onFocus).Background(t.focus)
	}

	mark := "  "
	if focused {
		mark = "› "
	}
	// The state glyph carries its own colour so a list of names can be read
	// for what is on without reading the names.
	glyph, glyphStyle := "", base
	switch r.check {
	case checkOn:
		glyph, glyphStyle = "● ", base.Foreground(t.good)
	case checkOff:
		glyph, glyphStyle = "○ ", base.Foreground(t.faint)
	}

	badge := library.Clean(r.badge)
	room := width - ansi.StringWidth(mark) - ansi.StringWidth(glyph) - ansi.StringWidth(badge) - 2
	name := ansi.Truncate(library.Clean(r.name), max(4, room), "…")
	gap := max(1, width-ansi.StringWidth(mark)-ansi.StringWidth(glyph)-ansi.StringWidth(name)-ansi.StringWidth(badge))

	rendered := base.Render(mark) + glyphStyle.Render(glyph)
	if matches := m.MatchesForItem(index); len(matches) > 0 {
		rendered += lipgloss.StyleRunes(name, matches, base.Foreground(t.accent).Underline(true), base.Foreground(t.text))
	} else {
		rendered += base.Foreground(t.text).Render(name)
	}
	rendered += base.Render(strings.Repeat(" ", gap))
	rendered += base.Foreground(t.tone(r.tone)).Render(badge)

	io.WriteString(w, rendered)
	if d.showDesc {
		desc := library.Clean(strings.ReplaceAll(r.desc, "\n", " "))
		io.WriteString(w, "\n"+base.Foreground(t.muted).Render(pad(ansi.Truncate("    "+desc, width, "…"), width)))
	}
}

// pad right-fills a line so a selection background covers the full width.
func pad(s string, width int) string {
	if n := width - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
