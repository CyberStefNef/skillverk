package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCLIGlobalActivationOutsideRepository(t *testing.T) {
	libraryHome, repo, source := cliFixture(t)
	account := filepath.Join(filepath.Dir(repo), "account")
	if err := os.MkdirAll(account, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", account)
	t.Setenv("USERPROFILE", account)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(account, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(account, ".codex"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(account, ".config"))
	base := []string{"--home", libraryHome, "--directory", account}
	invoke := func(args ...string) (string, error) {
		return capture(t, func() error { return run(append(append([]string{}, base...), args...)) })
	}
	if _, err := invoke("add", source); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("on", "probe", "--global", "--json"); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{".agents", ".claude"} {
		if _, err := os.Stat(filepath.Join(account, directory, "skills", "probe", "SKILL.md")); err != nil {
			t.Fatal(err)
		}
	}
	output, err := invoke("list", "--global", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Scope  string
		Skills []struct {
			Name         string
			Selected     bool
			GlobalStates map[string]string `json:"global_agents"`
		}
	}
	if err := json.Unmarshal([]byte(output), &listing); err != nil {
		t.Fatal(output, err)
	}
	found := false
	for _, skill := range listing.Skills {
		if skill.Name == "probe" {
			found = true
			if skill.Selected || skill.GlobalStates["codex"] != "active" || skill.GlobalStates["claude"] != "active" {
				t.Fatal(output)
			}
		}
	}
	if listing.Scope != "global" || !found {
		t.Fatal(output)
	}
	if _, err := invoke("off", "probe", "--global", "--harness", "codex", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(account, ".agents", "skills", "probe")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := invoke("retry", "--global", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("off", "probe", "--global", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(account, ".claude", "skills", "probe")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(repo, ".git", "skillverk-state.json")); !os.IsNotExist(err) {
		t.Fatal("global CLI changed repository state", err)
	}
}
