package library

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// ReviewSettings only controls setup assistance, independently of native skill
// activation and the providers' own authentication and configuration.
type ReviewSettings struct {
	Provider string            `json:"provider"`
	Models   map[string]string `json:"models,omitempty"`
}

func (s *Store) ReviewSettings() (ReviewSettings, error) {
	p := ReviewSettings{Provider: "auto", Models: map[string]string{}}
	e := readJSON(filepath.Join(s.Root, "review-settings.json"), &p)
	if errors.Is(e, os.ErrNotExist) {
		e = nil
	}
	if p.Models == nil {
		p.Models = map[string]string{}
	}
	if e == nil {
		e = validateReviewSettings(p)
	}
	return p, e
}
func validateReviewSettings(p ReviewSettings) error {
	if !slices.Contains([]string{"auto", "codex", "claude", "local"}, p.Provider) {
		return errors.New("review provider must be auto, codex, claude, or local")
	}
	return nil
}
func (s *Store) SaveReviewSettings(p ReviewSettings) error {
	if e := validateReviewSettings(p); e != nil {
		return e
	}
	return writeJSON(filepath.Join(s.Root, "review-settings.json"), p)
}
