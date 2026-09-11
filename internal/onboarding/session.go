package onboarding

import (
	"encoding/json"
	"github.com/CyberStefNef/skillverk/internal/library"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StartReview prepares instructions without scanning the user's repositories.
// Run in a private directory so the launch repository's development instructions
// do not become instructions for reviewing unrelated skills.
func StartReview(provider, model string, s *library.Store, repo string, roots []string) (*exec.Cmd, error) {
	if len(roots) == 0 {
		roots = library.DefaultSetupRoots()
		if repo != "" {
			roots = append(roots, repo)
		}
	}
	var absolute []string
	for _, root := range roots {
		p, e := filepath.Abs(library.Expand(root))
		if e != nil {
			return nil, e
		}
		absolute = append(absolute, p)
	}
	dir, e := os.MkdirTemp(s.Root, "review-")
	if e != nil {
		return nil, e
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(dir)
		}
	}()
	executable, e := os.Executable()
	if e != nil {
		return nil, e
	}
	request := struct {
		Library      string   `json:"library"`
		GlobalSkills []string `json:"global_skill_directories"`
		Roots        []string `json:"roots"`
		Executable   string   `json:"executable"`
	}{s.Root, s.SetupGlobalDirectories(), absolute, executable}
	body, e := json.MarshalIndent(request, "", "  ")
	if e != nil {
		return nil, e
	}
	if e = os.WriteFile(filepath.Join(dir, "request.json"), body, 0600); e != nil {
		return nil, e
	}
	skill, _ := files.ReadFile("skill/SKILL.md")
	instructions := string(skill)
	if strings.HasPrefix(instructions, "---\n") {
		if end := strings.Index(instructions[4:], "\n---\n"); end >= 0 {
			instructions = instructions[4+end+5:]
		}
	}
	guide := filepath.Join(dir, "instructions.md")
	if e = os.WriteFile(guide, []byte(instructions), 0600); e != nil {
		return nil, e
	}
	cmd, e := ModelCommand(provider, model, guide, dir)
	if e != nil {
		return nil, e
	}
	ok = true
	return cmd, nil
}
