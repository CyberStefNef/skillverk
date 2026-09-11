package onboarding

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed skill/SKILL.md skill/agents/openai.yaml
var files embed.FS

// Install refuses foreign content and linked paths. It stages the complete
// companion before replacing a previous Skillverk installation.
func Install() ([]string, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return nil, e
	}
	codex := os.Getenv("CODEX_HOME")
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = filepath.Join(home, ".claude")
	}
	var installed []string
	for _, base := range []string{codex, claude} {
		if _, e = os.Stat(base); errors.Is(e, os.ErrNotExist) {
			continue
		} else if e != nil {
			return installed, e
		}
		dest, e := filepath.Abs(filepath.Join(base, "skills", "skillverk-setup"))
		if e != nil {
			return installed, e
		}
		if e = installAt(dest); e != nil {
			return installed, e
		}
		installed = append(installed, dest)
	}
	return installed, nil
}
func installAt(dest string) error {
	for p := dest; ; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e == nil && i.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("linked companion path preserved: %s", p)
		}
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	existed := false
	if _, e := os.Lstat(dest); e == nil {
		existed = true
		marker, e := os.ReadFile(filepath.Join(dest, ".skillverk-owned"))
		if e != nil || string(marker) != "1\n" {
			return fmt.Errorf("existing companion preserved: %s", dest)
		}
		e = filepath.WalkDir(dest, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(dest, p)
			if rel != "." && rel != "agents" && rel != "SKILL.md" && rel != ".skillverk-owned" && rel != filepath.Join("agents", "openai.yaml") {
				return fmt.Errorf("extra companion content preserved: %s", p)
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("linked companion file preserved: %s", p)
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	parent := filepath.Dir(dest)
	if e := os.MkdirAll(parent, 0755); e != nil {
		return e
	}
	stage, e := os.MkdirTemp(parent, ".skillverk-companion-")
	if e != nil {
		return e
	}
	removeStage := true
	defer func() {
		if removeStage {
			_ = os.RemoveAll(stage)
		}
	}()
	next := filepath.Join(stage, "next")
	if e = os.MkdirAll(filepath.Join(next, "agents"), 0755); e != nil {
		return e
	}
	for _, rel := range []string{"SKILL.md", "agents/openai.yaml"} {
		body, e := files.ReadFile("skill/" + rel)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(next, filepath.FromSlash(rel)), body, 0644); e != nil {
			return e
		}
	}
	if e = os.WriteFile(filepath.Join(next, ".skillverk-owned"), []byte("1\n"), 0600); e != nil {
		return e
	}
	backup := filepath.Join(stage, "previous")
	if existed {
		if e = os.Rename(dest, backup); e != nil {
			return e
		}
	}
	if e = os.Rename(next, dest); e != nil {
		if existed {
			if er := os.Rename(backup, dest); er != nil { // Retain custody if restoration fails.
				removeStage = false
				retained := backup
				return fmt.Errorf("%w; restore failed: %v; original retained at %s", e, er, retained)
			}
		}
		return e
	}
	return nil
}
func Prompt(instructions string) string {
	return "Help me organize my existing skills. Read the review instructions at " + fmt.Sprintf("%q", instructions) + ", explore the relevant folders, and recommend changes before asking me to apply them."
}
