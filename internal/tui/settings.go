package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/CyberStefNef/skillverk/internal/library"
	"github.com/CyberStefNef/skillverk/internal/onboarding"
	"slices"
	"strings"
)

var providers = []string{"auto", "codex", "claude", "local"}

func providerLabel(provider string) string {
	switch provider {
	case "auto":
		return "Automatic"
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "local":
		return "No AI, review in Skillverk"
	default:
		return provider
	}
}

func modelLabel(a *app) string {
	provider := onboarding.Resolve(a.data.review.Provider)
	if provider == "local" {
		return "not used for local review"
	}
	if name := a.data.review.Models[provider]; name != "" {
		return name
	}
	return "the provider's configured default"
}

// reviewSummary is the one-line answer to "how will my skills be reviewed?".
func reviewSummary(a *app) string {
	provider := onboarding.Resolve(a.data.review.Provider)
	if provider == "local" {
		return "in Skillverk, without AI"
	}
	return "with " + providerLabel(provider) + " · " + modelLabel(a)
}

// newSettingsPage chooses how setup review runs. Each change saves at once,
// because there is nothing here that needs a separate apply step.
func newSettingsPage(a *app) *listPage {
	p := newList(a, "Review settings", "How Skillverk reviews the skills already on this machine.", true, false)
	fill := func(a *app) {
		p.setRows([]row{
			{id: "provider", name: "Provider", badge: providerLabel(a.data.review.Provider),
				desc: "Automatic uses Codex, then Claude Code, whichever is installed."},
			{id: "model", name: "Model", badge: modelLabel(a),
				desc: "Uses your existing sign-in and the provider's own model settings."},
		})
	}
	fill(a)
	p.refreshed = func(a *app, _ *listPage) tea.Cmd { fill(a); return nil }
	p.notes = func(a *app, _ *listPage) []string {
		return []string{a.theme.Muted.Render("Setup review will run " + reviewSummary(a) + ".")}
	}
	p.enter = func(a *app, p *listPage) tea.Cmd {
		r, ok := p.focus()
		if !ok {
			return nil
		}
		if r.id == "provider" {
			next := providers[(slices.Index(providers, a.data.review.Provider)+1)%len(providers)]
			settings := a.data.review
			settings.Provider = next
			return save(a, settings, fill)
		}
		provider := onboarding.Resolve(a.data.review.Provider)
		if provider == "local" {
			return warn("Install Codex or Claude Code, or choose an installed provider first.")
		}
		return push(newInputPage(a, "Review model",
			"A model name the "+providerLabel(provider)+" CLI accepts. Leave blank for its default.",
			a.data.review.Models[provider], "leave blank for the default",
			func(a *app, value string) tea.Cmd {
				settings := a.data.review
				if settings.Models == nil {
					settings.Models = map[string]string{}
				}
				settings.Models[provider] = strings.TrimSpace(value)
				return tea.Sequence(pop, save(a, settings, fill))
			}))
	}
	return p
}

func save(a *app, settings library.ReviewSettings, fill func(*app)) tea.Cmd {
	if err := a.store.SaveReviewSettings(settings); err != nil {
		if previous, e := a.store.ReviewSettings(); e == nil {
			a.data.review = previous
		}
		fill(a)
		return failure(err)
	}
	a.data.review = settings
	fill(a)
	return done("Review settings saved.")
}
