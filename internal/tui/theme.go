package tui

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"image/color"
)

// theme resolves every colour the interface uses against the terminal
// background, so one definition serves both light and dark terminals.
type theme struct {
	dark bool

	// Raw colours, kept so composed styles (list rows) can re-tint against a
	// selection background without losing it.
	accent, text, muted, faint, focus, onFocus, good, warn, bad color.Color

	Brand   lipgloss.Style
	Heading lipgloss.Style
	Lead    lipgloss.Style
	Muted   lipgloss.Style
	Rule    lipgloss.Style
	Body    lipgloss.Style
	Key     lipgloss.Style

	Cursor lipgloss.Style
	Mark   lipgloss.Style
	Match  lipgloss.Style

	TabOn  lipgloss.Style
	TabOff lipgloss.Style

	Good lipgloss.Style
	Warn lipgloss.Style
	Bad  lipgloss.Style

	Help  help.Styles
	Input textinput.Styles
	List  list.Styles
}

func newTheme(dark bool) theme {
	c := lipgloss.LightDark(dark)
	var (
		accent  = c(lipgloss.Color("#0F766E"), lipgloss.Color("#5FD7B7"))
		text    = c(lipgloss.Color("#1B2429"), lipgloss.Color("#E6EDF3"))
		muted   = c(lipgloss.Color("#5A6872"), lipgloss.Color("#8A9AA6"))
		faint   = c(lipgloss.Color("#9AA7B0"), lipgloss.Color("#5A6872"))
		focus   = c(lipgloss.Color("#DCEFEA"), lipgloss.Color("#1F3A44"))
		good    = c(lipgloss.Color("#15803D"), lipgloss.Color("#7EE787"))
		warn    = c(lipgloss.Color("#9A6700"), lipgloss.Color("#F0C674"))
		bad     = c(lipgloss.Color("#B42318"), lipgloss.Color("#FF7B72"))
		onFocus = c(lipgloss.Color("#0B3B33"), lipgloss.Color("#F2FBF8"))
	)
	base := lipgloss.NewStyle()
	t := theme{
		dark:    dark,
		accent:  accent,
		text:    text,
		muted:   muted,
		faint:   faint,
		focus:   focus,
		onFocus: onFocus,
		good:    good,
		warn:    warn,
		bad:     bad,
		Brand:   base.Foreground(accent).Bold(true),
		Heading: base.Foreground(text).Bold(true),
		Lead:    base.Foreground(muted),
		Muted:   base.Foreground(muted),
		Rule:    base.Foreground(faint),
		Body:    base.Foreground(text),
		Key:     base.Foreground(accent),
		Cursor:  base.Foreground(onFocus).Background(focus),
		Mark:    base.Foreground(accent).Bold(true),
		Match:   base.Foreground(accent).Underline(true),
		TabOn:   base.Foreground(onFocus).Background(focus).Bold(true).Padding(0, 1),
		TabOff:  base.Foreground(muted).Padding(0, 1),
		Good:    base.Foreground(good),
		Warn:    base.Foreground(warn),
		Bad:     base.Foreground(bad),
	}
	t.Help = help.DefaultStyles(dark)
	t.Help.ShortKey = t.Help.ShortKey.Foreground(accent)
	t.Help.FullKey = t.Help.FullKey.Foreground(accent)
	t.Help.ShortDesc = t.Help.ShortDesc.Foreground(muted)
	t.Help.FullDesc = t.Help.FullDesc.Foreground(muted)
	t.Help.ShortSeparator = t.Help.ShortSeparator.Foreground(faint)
	t.Help.FullSeparator = t.Help.FullSeparator.Foreground(faint)
	t.Help.Ellipsis = t.Help.Ellipsis.Foreground(faint)
	t.Input = textinput.DefaultStyles(dark)
	t.Input.Focused.Prompt = base.Foreground(accent)
	t.Input.Focused.Text = base.Foreground(text)
	t.Input.Focused.Placeholder = base.Foreground(faint)
	t.List = list.DefaultStyles(dark)
	t.List.NoItems = base.Foreground(muted)
	t.List.PaginationStyle = base.Foreground(faint).PaddingLeft(2)
	t.List.ActivePaginationDot = base.Foreground(accent)
	t.List.InactivePaginationDot = base.Foreground(faint)
	t.List.DividerDot = base.Foreground(faint)
	t.List.ArabicPagination = base.Foreground(faint)
	return t
}

// level classifies a status line so it reads correctly at a glance.
type level int

const (
	levelInfo level = iota
	levelDone
	levelWarn
	levelError
)

// tone maps a level to its colour.
func (t theme) tone(l level) color.Color {
	switch l {
	case levelDone:
		return t.good
	case levelWarn:
		return t.warn
	case levelError:
		return t.bad
	default:
		return t.muted
	}
}

func (t theme) status(l level) (lipgloss.Style, string) {
	switch l {
	case levelDone:
		return t.Good, "✓ "
	case levelWarn:
		return t.Warn, "! "
	case levelError:
		return t.Bad, "✗ "
	default:
		return t.Muted, ""
	}
}
