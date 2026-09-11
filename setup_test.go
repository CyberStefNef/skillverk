package main

import (
	"encoding/json"
	"github.com/CyberStefNef/skillverk/internal/library"
	"os"
	"path/filepath"
	"testing"
)

func TestSetupCLIPlanRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	home := filepath.Join(root, "library")
	plan := filepath.Join(root, "plan.json")
	if e := run([]string{"setup", "--home", home, "--scan", "--plan", plan, "--json", root}); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(plan)
	if e != nil {
		t.Fatal(e)
	}
	var p library.SetupPlan
	if e = json.Unmarshal(b, &p); e != nil {
		t.Fatal(e)
	}
	if p.Library != home || p.Snapshot == "" {
		t.Fatal("invalid preview")
	}
	if e = run([]string{"setup", "--home", home, "--apply", plan, "--json"}); e == nil {
		t.Fatal("approval not required")
	}
	if e = run([]string{"setup", "--home", home, "--apply", plan, "--yes", "--json"}); e != nil {
		t.Fatal(e)
	}
	if e = run([]string{"setup", "--home", home, "--apply", plan, "--scan"}); e == nil {
		t.Fatal("incompatible options accepted")
	}
}

// The stored review setting calls Skillverk's own review "local", so the flag
// has to accept the same word. Without a terminal it stops before the picker.
func TestSetupProviderLocalIsAccepted(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	home := filepath.Join(root, "library")
	if e := run([]string{"setup", "--home", home, "--provider", "local"}); e != nil &&
		e.Error() != "provider review requires an interactive terminal" {
		t.Fatal(e)
	}
	e := run([]string{"setup", "--home", home, "--provider", "nonsense"})
	if e == nil || e.Error() != "choose auto, codex, claude, or local" {
		t.Fatalf("unknown provider accepted: %v", e)
	}
}
