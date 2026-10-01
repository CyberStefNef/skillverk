package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func globalFixture(t *testing.T) (*Store, string, string) {
	t.Helper()
	s, root, source := fixture(t)
	home := filepath.Join(root, "account")
	must(t, os.MkdirAll(home, 0755))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude-profile"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	return s, root, source
}

func globalPath(t *testing.T, agent, name string) string {
	t.Helper()
	root, err := GlobalRoot(agent)
	must(t, err)
	return filepath.Join(root, name)
}

func TestGlobalActivationLifecycleAndRepositoryIndependence(t *testing.T) {
	s, root, source := globalFixture(t)
	work := repo(t, root, "work")
	_, err := s.Select(work, []string{"review"}, true)
	must(t, err)
	before, err := os.ReadFile(filepath.Join(work, ".git", "skillverk-state.json"))
	must(t, err)
	target, err := s.Path("review")
	must(t, err)
	_, err = s.SelectGlobal([]string{"review"}, nil, true)
	must(t, err)
	for _, agent := range DefaultAgents {
		checkLink(t, globalPath(t, agent, "review"), target)
		checkLink(t, LinkPath(work, agent, "review"), target)
	}
	after, err := os.ReadFile(filepath.Join(work, ".git", "skillverk-state.json"))
	must(t, err)
	if string(before) != string(after) {
		t.Fatal("global activation changed repository state")
	}
	info, err := s.Find(work, "review")
	must(t, err)
	if !info.Selected || !info.HasScope("global") || info.GlobalStates["codex"] != "active" || info.GlobalStates["claude"] != "active" {
		t.Fatal(info)
	}
	_, err = s.SelectGlobal([]string{"review"}, nil, true)
	must(t, err)
	d, err := s.load()
	must(t, err)
	if len(d.Links) != 4 {
		t.Fatal("repeated activation added records", d.Links)
	}
	skill(t, filepath.Dir(source), "review", "updated")
	_, err = s.Refresh([]string{"review"})
	must(t, err)
	for _, agent := range DefaultAgents {
		body, err := os.ReadFile(filepath.Join(globalPath(t, agent, "review"), "resources", "data.txt"))
		must(t, err)
		if string(body) != "updated" {
			t.Fatal(string(body))
		}
	}
	_, err = s.Select(work, []string{"review"}, false)
	must(t, err)
	checkLink(t, globalPath(t, "codex", "review"), target)
	_, err = s.SelectGlobal([]string{"review"}, []string{"codex"}, false)
	must(t, err)
	absent(t, globalPath(t, "codex", "review"))
	checkLink(t, globalPath(t, "claude", "review"), target)
	_, err = s.SelectGlobal([]string{"review"}, []string{"opencode"}, true)
	must(t, err)
	checkLink(t, globalPath(t, "opencode", "review"), target)
	_, err = s.SelectGlobal([]string{"review"}, nil, false)
	must(t, err)
	for _, agent := range []string{"codex", "claude", "opencode"} {
		absent(t, globalPath(t, agent, "review"))
	}
}

func TestGlobalConflictsPreservedAndRetryIsExplicit(t *testing.T) {
	s, _, _ := globalFixture(t)
	claude := globalPath(t, "claude", "review")
	put(t, claude, "foreign file")
	results, err := s.SelectGlobal([]string{"review"}, nil, true)
	if err == nil || len(results) != 2 || results[0].Error != "" || results[1].Error == "" {
		t.Fatal(results, err)
	}
	body, err := os.ReadFile(claude)
	must(t, err)
	if string(body) != "foreign file" {
		t.Fatal("foreign installation changed")
	}
	if _, err = s.Doctor(""); err == nil {
		t.Fatal("doctor hid a failed global intent")
	}
	_, err = s.Reconcile("")
	must(t, err)
	body, err = os.ReadFile(claude)
	must(t, err)
	if string(body) != "foreign file" {
		t.Fatal("reconcile modified an unowned path")
	}
	must(t, os.Remove(claude))
	_, err = s.RetryGlobal()
	must(t, err)
	target, err := s.Path("review")
	must(t, err)
	checkLink(t, claude, target)
	codex := globalPath(t, "codex", "review")
	must(t, os.Remove(codex))
	skill(t, filepath.Dir(codex), "review", "external replacement")
	if _, err = s.SelectGlobal([]string{"review"}, []string{"codex"}, false); err == nil {
		t.Fatal("replacement was silently removed")
	}
	body, err = os.ReadFile(filepath.Join(codex, "resources", "data.txt"))
	must(t, err)
	if string(body) != "external replacement" {
		t.Fatal("replacement changed")
	}
	checkLink(t, claude, target)
	must(t, os.RemoveAll(codex))
	_, err = s.RetryGlobal()
	must(t, err)
	absent(t, codex)
	checkLink(t, claude, target)
}

func TestGlobalOffClearsUnownedFailedIntent(t *testing.T) {
	s, _, _ := globalFixture(t)
	path := globalPath(t, "claude", "review")
	put(t, path, "foreign")
	if _, err := s.SelectGlobal([]string{"review"}, []string{"claude"}, true); err == nil {
		t.Fatal("foreign path accepted")
	}
	_, err := s.SelectGlobal([]string{"review"}, nil, false)
	must(t, err)
	body, err := os.ReadFile(path)
	must(t, err)
	if string(body) != "foreign" {
		t.Fatal("foreign path changed")
	}
	d, err := s.load()
	must(t, err)
	if len(d.Links) != 0 {
		t.Fatal("failed intent was not cleared", d.Links)
	}
}

func TestGlobalActivationPreservesLinkedAncestors(t *testing.T) {
	s, root, _ := globalFixture(t)
	outside := filepath.Join(root, "outside")
	must(t, os.MkdirAll(outside, 0755))
	claude := filepath.Dir(globalPath(t, "claude", "review"))
	must(t, os.MkdirAll(filepath.Dir(claude), 0755))
	must(t, directoryLink(outside, claude))
	if _, err := s.SelectGlobal([]string{"review"}, []string{"claude"}, true); err == nil || !strings.Contains(err.Error(), "linked global parent") {
		t.Fatal(err)
	}
	absent(t, filepath.Join(outside, "review"))
}

func TestGlobalRemovalPreservesRedirectedAncestor(t *testing.T) {
	s, root, _ := globalFixture(t)
	_, err := s.SelectGlobal([]string{"review"}, []string{"claude"}, true)
	must(t, err)
	path := globalPath(t, "claude", "review")
	parent := filepath.Dir(path)
	saved := filepath.Join(root, "saved-skills")
	must(t, os.Rename(parent, saved))
	outside := filepath.Join(root, "outside")
	must(t, os.MkdirAll(outside, 0755))
	target, err := s.Path("review")
	must(t, err)
	must(t, directoryLink(target, filepath.Join(outside, "review")))
	must(t, directoryLink(outside, parent))
	if _, err := s.SelectGlobal([]string{"review"}, nil, false); err == nil {
		t.Fatal("redirected parent accepted")
	}
	checkLink(t, filepath.Join(outside, "review"), target)
	must(t, os.Remove(parent))
	must(t, os.Rename(saved, parent))
	_, err = s.RetryGlobal()
	must(t, err)
	absent(t, path)
	checkLink(t, filepath.Join(outside, "review"), target)
}

func TestGlobalDeletionAndLegacyProjectAliases(t *testing.T) {
	s, root, _ := globalFixture(t)
	work := repo(t, root, "work")
	nested := skill(t, filepath.Join(work, "nested", ".claude", "skills"), "review", "one")
	migration := *s
	migration.ScanRoots = []ScanRoot{{filepath.Dir(nested), "global", "claude"}}
	_, err := migration.ShareGlobal(nested, true)
	must(t, err)
	target, err := s.Path("review")
	must(t, err)
	_, err = s.SelectGlobal([]string{"review"}, nil, true)
	must(t, err)
	_, err = s.SelectGlobal([]string{"review"}, nil, false)
	must(t, err)
	checkLink(t, nested, target)
	_, err = s.SelectGlobal([]string{"review"}, nil, true)
	must(t, err)
	_, err = s.Select(work, []string{"review"}, true)
	must(t, err)
	_, err = s.Delete("review", true)
	must(t, err)
	for _, agent := range DefaultAgents {
		absent(t, globalPath(t, agent, "review"))
		absent(t, LinkPath(work, agent, "review"))
	}
	absent(t, nested)
}

func TestGlobalRequestValidatedBeforeMutation(t *testing.T) {
	s, _, _ := globalFixture(t)
	if _, err := s.SelectGlobal([]string{"review", "missing"}, nil, true); err == nil {
		t.Fatal("missing skill accepted")
	}
	d, err := s.load()
	must(t, err)
	if len(d.Links) != 0 {
		t.Fatal("invalid request partially applied")
	}
	if _, err := s.SelectGlobal([]string{"review"}, []string{"unknown"}, true); err == nil {
		t.Fatal("unknown harness accepted")
	}
	home, err := os.UserHomeDir()
	must(t, err)
	root, err := GlobalRoot("codex")
	must(t, err)
	if root != filepath.Join(home, ".agents", "skills") {
		t.Fatal(root)
	}
	root, err = GlobalRoot("opencode")
	must(t, err)
	if root != filepath.Join(home, "config", "opencode", "skills") {
		t.Fatal(root)
	}
}
