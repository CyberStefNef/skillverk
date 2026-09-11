package tui

import "charm.land/bubbles/v2/key"

// keys are the bindings every screen shares. Screens add their own on top;
// nothing is bound to a letter that only appears in the manual.
type keySet struct {
	Up      key.Binding
	Down    key.Binding
	Choose  key.Binding
	Toggle  key.Binding
	All     key.Binding
	Search  key.Binding
	Actions key.Binding
	Back    key.Binding
	Help    key.Binding
	Quit    key.Binding
	Yes     key.Binding
	Cancel  key.Binding
}

var keys = keySet{
	Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	Choose:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose")),
	Toggle:  key.NewBinding(key.WithKeys("space", " "), key.WithHelp("space", "toggle")),
	All:     key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all/none")),
	Search:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
	Actions: key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "menu")),
	Back:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	Yes:     key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "confirm")),
	Cancel:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
}

// bind is shorthand for a screen-specific binding.
func bind(k, help string, extra ...string) key.Binding {
	return key.NewBinding(key.WithKeys(append([]string{k}, extra...)...), key.WithHelp(k, help))
}

// columns splits bindings into roughly equal groups for the expanded help view.
func columns(bindings []key.Binding, n int) [][]key.Binding {
	if len(bindings) == 0 {
		return nil
	}
	size := (len(bindings) + n - 1) / n
	var out [][]key.Binding
	for i := 0; i < len(bindings); i += size {
		out = append(out, bindings[i:min(i+size, len(bindings))])
	}
	return out
}
