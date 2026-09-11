package onboarding

import (
	"encoding/json"
	"github.com/CyberStefNef/skillverk/internal/library"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentFirstHandoff(t *testing.T) {
	root := t.TempDir()
	provider := filepath.Join(root, "codex")
	if e := os.WriteFile(provider, []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\"\n"), 0755); e != nil {
		t.Fatal(e)
	}
	s, e := library.New(filepath.Join(root, "library"))
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", root)
	// An unavailable root must not block agent exploration at launch.
	requested := filepath.Join(root, "unavailable")
	cmd, e := StartReview("codex", "chosen", s, root, []string{requested})
	if e != nil {
		t.Fatal(e)
	}
	if cmd.Dir == root || !strings.HasPrefix(cmd.Dir, s.Root+string(os.PathSeparator)) {
		t.Fatal("agent inherited launch repository", cmd.Dir)
	}
	prompt := cmd.Args[len(cmd.Args)-1]
	if strings.Contains(prompt, "---") || strings.Contains(prompt, "authentication") || len(prompt) > 600 {
		t.Fatal("verbose handoff", prompt)
	}
	body, e := os.ReadFile(filepath.Join(cmd.Dir, "request.json"))
	if e != nil {
		t.Fatal(e)
	}
	var request struct {
		Library    string
		Roots      []string
		Executable string
	}
	if e = json.Unmarshal(body, &request); e != nil {
		t.Fatal(e)
	}
	if request.Library != s.Root || len(request.Roots) != 1 || request.Roots[0] != requested || request.Executable == "" {
		t.Fatal(request)
	}
	guide, e := os.ReadFile(filepath.Join(cmd.Dir, "instructions.md"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.HasPrefix(string(guide), "---") || !strings.Contains(string(guide), "Investigate each skipped location") {
		t.Fatal("invalid agent instructions")
	}
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(out), cmd.Dir) || !strings.Contains(string(out), "--model\nchosen") {
		t.Fatal("provider handoff failed", string(out))
	}
}
