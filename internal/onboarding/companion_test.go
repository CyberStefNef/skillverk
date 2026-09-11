package onboarding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAndForeignPreservation(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	claude := filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	if e := os.MkdirAll(codex, 0755); e != nil {
		t.Fatal(e)
	}
	paths, e := Install()
	if e != nil || len(paths) != 1 {
		t.Fatal(paths, e)
	}
	if _, e = Install(); e != nil {
		t.Fatal(e)
	}
	foreign := filepath.Join(paths[0], "notes")
	if e = os.WriteFile(foreign, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Install(); e == nil {
		t.Fatal("foreign file overwritten")
	}
	body, e := os.ReadFile(foreign)
	if e != nil || string(body) != "keep" {
		t.Fatal("foreign content lost")
	}
}
func TestInstallRefusesLinkedDestination(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "skillverk-setup")
	target := filepath.Join(root, "target")
	_ = os.Mkdir(target, 0755)
	if e := os.Symlink(target, dest); e != nil {
		t.Skip(e)
	}
	if e := installAt(dest); e == nil {
		t.Fatal("linked destination accepted")
	}
	if _, e := os.Stat(filepath.Join(target, "SKILL.md")); !os.IsNotExist(e) {
		t.Fatal("wrote through link")
	}
}
func TestProviderCommandUsesInteractivePrompt(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", root)
	for _, provider := range []string{"codex", "claude"} {
		path := filepath.Join(root, provider)
		if e := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); e != nil {
			t.Fatal(e)
		}
		cmd, e := ModelCommand(provider, "", "/tmp/plan with spaces.json", root)
		if e != nil {
			t.Fatal(e)
		}
		if len(cmd.Args) != 2 || cmd.Dir != root || cmd.Env != nil || !strings.Contains(cmd.Args[1], "plan with spaces.json") {
			t.Fatalf("unexpected command: %+v", cmd)
		}
		if strings.Contains(cmd.Args[1], "skip-permissions") {
			t.Fatal("permissions bypass")
		}
	}
	if _, e := ModelCommand("other", "", "plan", root); e == nil {
		t.Fatal("unknown provider accepted")
	}
	_ = os.Remove(filepath.Join(root, "codex"))
	if _, e := ModelCommand("codex", "", "plan", root); e == nil {
		t.Fatal("missing provider accepted")
	}
}

func TestAutomaticProviderSelectionAndModelOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", root)
	if Resolve("auto") != "local" {
		t.Fatal("missing CLIs should use local review")
	}
	executable := func(name string) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(root, name), []byte("#!/bin/sh\nexit 0\n"), 0755); e != nil {
			t.Fatal(e)
		}
	}
	executable("claude")
	if Resolve("auto") != "claude" {
		t.Fatal("Claude not detected")
	}
	executable("codex")
	if Resolve("auto") != "codex" {
		t.Fatal("tie-break changed")
	}
	if Resolve("claude") != "claude" || Resolve("local") != "local" {
		t.Fatal("explicit preference ignored")
	}
	cmd, e := ModelCommand("auto", "chosen-model", "plan.json", root)
	if e != nil {
		t.Fatal(e)
	}
	if len(cmd.Args) != 4 || cmd.Args[1] != "--model" || cmd.Args[2] != "chosen-model" {
		t.Fatal(cmd.Args)
	}
	cmd, e = ModelCommand("claude", "", "plan.json", root)
	if e != nil {
		t.Fatal(e)
	}
	if len(cmd.Args) != 2 {
		t.Fatal("provider default overridden", cmd.Args)
	}
}
