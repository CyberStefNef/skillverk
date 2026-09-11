package library

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestHarnessActivationLifecycle(t *testing.T) {
	for _, agent := range Agents {
		t.Run(agent, func(t *testing.T) {
			s, root, src := fixture(t)
			r := repo(t, root, "project")
			_, e := s.SetAgents(r, []string{agent, agent})
			must(t, e)
			st, e := ReadState(r)
			must(t, e)
			if !slices.Equal(st.Agents, []string{agent}) {
				t.Fatal(st.Agents)
			}
			_, e = s.Select(r, []string{"review"}, true)
			must(t, e)
			target, e := s.Path("review")
			must(t, e)
			checkLink(t, LinkPath(r, agent, "review"), target)
			for _, other := range Agents {
				if other != agent {
					absent(t, LinkPath(r, other, "review"))
				}
			}
			status, e := gitAt(r, "status", "--porcelain")
			must(t, e)
			if status != "" {
				t.Fatal(status)
			}
			skill(t, filepath.Dir(src), "review", "two")
			results, e := s.Refresh([]string{"review"})
			must(t, e)
			for _, result := range results {
				if result.Error != "" {
					t.Fatal(result)
				}
			}
			b, e := os.ReadFile(filepath.Join(LinkPath(r, agent, "review"), "resources", "data.txt"))
			must(t, e)
			if string(b) != "two" {
				t.Fatal("refresh not visible", string(b))
			}
			_, e = s.Select(r, []string{"review"}, false)
			must(t, e)
			absent(t, LinkPath(r, agent, "review"))
			_, e = s.Select(r, []string{"review"}, true)
			must(t, e)
			_, e = s.Delete("review", true)
			must(t, e)
			absent(t, LinkPath(r, agent, "review"))
		})
	}
}
func TestHarnessSwitchPreservesConflictsAndDefaults(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "project")
	st, e := ReadState(r)
	must(t, e)
	if !slices.Equal(st.Agents, DefaultAgents) {
		t.Fatal(st.Agents)
	}

	_, e = s.Select(r, []string{"review"}, true)
	must(t, e)
	local := skill(t, filepath.Join(r, ".opencode", "skills"), "review", "local")
	_, e = s.SetAgents(r, []string{"opencode", "cursor"})
	if e == nil {
		t.Fatal("conflict ignored")
	}
	b, e := os.ReadFile(filepath.Join(local, "resources", "data.txt"))
	must(t, e)
	if string(b) != "local" {
		t.Fatal("original changed")
	}
	absent(t, LinkPath(r, "codex", "review"))
	absent(t, LinkPath(r, "claude", "review"))
	target, e := s.Path("review")
	must(t, e)
	checkLink(t, LinkPath(r, "cursor", "review"), target)
	before, e := ReadState(r)
	must(t, e)
	if _, e = s.SetAgents(r, []string{"unknown"}); e == nil {
		t.Fatal("invalid harness accepted")
	}
	after, e := ReadState(r)
	must(t, e)
	if !slices.Equal(before.Agents, after.Agents) {
		t.Fatal("invalid choice changed state")
	}
}

func TestHarnessTakesControlOfMigratedNativePath(t *testing.T) {
	s, root, _ := ecosystemFixture(t)
	r := repo(t, root, "project")
	native := skill(t, filepath.Join(r, ".opencode", "skills"), "client", "fixture")
	p, e := s.ScanSetup(context.Background(), []string{r})
	must(t, e)
	if len(p.Items) != 1 {
		t.Fatal(p.Items)
	}
	p.Items[0].Action = "share"
	_, e = s.ApplySetup(context.Background(), p, true)
	must(t, e)
	_, e = s.SetAgents(r, []string{"opencode"})
	must(t, e)
	_, e = s.Select(r, []string{"client"}, true)
	must(t, e)
	target, e := s.Path("client")
	must(t, e)
	checkLink(t, native, target)
	_, e = s.Select(r, []string{"client"}, false)
	must(t, e)
	absent(t, native)
}

func TestCompatibilityPathsDoNotClaimActivation(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "project")
	_, e := s.SetAgents(r, []string{"codex"})
	must(t, e)
	_, e = s.Select(r, []string{"review"}, true)
	must(t, e)
	rows, e := s.Catalog(r)
	must(t, e)
	sk := rows[0]
	for _, agent := range []string{"opencode", "cursor", "gemini", "factory"} {
		if !slices.Contains(sk.CompatibilityPaths[agent], LinkPath(r, "codex", "review")) {
			t.Fatal(agent, sk.CompatibilityPaths)
		}
		if sk.States[agent] == "active" {
			t.Fatal("claimed native activation", agent)
		}
	}
	if sk.Status() != "active" {
		t.Fatal("compatibility observation changed managed status", sk.Status())
	}
	if !strings.Contains(sk.Details(), "may also discover") {
		t.Fatal(sk.Details())
	}
	_, e = s.Select(r, []string{"review"}, false)
	must(t, e)
	rows, e = s.Catalog(r)
	must(t, e)
	if len(rows[0].CompatibilityPaths) != 0 {
		t.Fatal("stale visibility", rows[0].CompatibilityPaths)
	}
}
func TestNativeMetadataLimitsAndRefreshRecovery(t *testing.T) {
	s, root, src := fixture(t)
	r := repo(t, root, "project")
	_, e := s.SetAgents(r, []string{"opencode"})
	must(t, e)
	_, e = s.Select(r, []string{"review"}, true)
	must(t, e)
	target, e := s.Path("review")
	must(t, e)
	before, e := os.ReadFile(filepath.Join(target, "SKILL.md"))
	must(t, e)
	raw := "---\nname: review\ndescription: " + strings.Repeat("é", 1025) + "\n---\nFixture"
	put(t, filepath.Join(src, "SKILL.md"), raw)
	results, e := s.Refresh([]string{"review"})
	if e == nil || !strings.Contains(e.Error(), "1024") || len(results) != 1 {
		t.Fatal(results, e)
	}
	after, e := os.ReadFile(filepath.Join(target, "SKILL.md"))
	must(t, e)
	if string(after) != string(before) {
		t.Fatal("invalid update replaced active content")
	}
	// Import retains invalid metadata for review; activation must refuse it.
	bad := skill(t, filepath.Join(root, "other"), "client", "fixture")
	put(t, filepath.Join(bad, "SKILL.md"), strings.Replace(raw, "name: review", "name: client", 1))
	c, e := OpenCollection(bad)
	must(t, e)
	_, e = s.Publish(c, nil, false)
	c.Close()
	must(t, e)
	_, e = s.Select(r, []string{"client"}, true)
	if e == nil || !strings.Contains(e.Error(), "1024") {
		t.Fatal(e)
	}
	absent(t, LinkPath(r, "opencode", "client"))
	// Count characters, not UTF-8 bytes, and retain the exact boundary.
	put(t, filepath.Join(bad, "SKILL.md"), "---\nname: client\ndescription: "+strings.Repeat("é", 1024)+"\n---\nFixture")
	must(t, validateNativeMetadata(bad))
}

// Opt in with SKILLVERK_TEST_OPENCODE=/absolute/path/to/opencode.
// This exercises native discovery only, with plugins disabled and an isolated home.
func TestOpenCodeNativeDiscovery(t *testing.T) {
	binary := os.Getenv("SKILLVERK_TEST_OPENCODE")
	if binary == "" {
		t.Skip("native OpenCode check is opt-in")
	}
	s, root, home := ecosystemFixture(t)
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(key, filepath.Join(home, key))
	}
	r := repo(t, root, "repository with spaces")
	src := filepath.Join(root, "source", "review")
	inventory := func() []struct{ Name, Location, Content string } {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "debug", "skill", "--pure")
		cmd.Dir = r
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "OPENCODE_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		raw, e := cmd.Output()
		must(t, e)
		var rows []struct{ Name, Location, Content string }
		must(t, json.Unmarshal(raw, &rows))
		return rows
	}
	find := func() (string, string) {
		for _, row := range inventory() {
			if row.Name == "review" {
				return row.Location, row.Content
			}
		}
		return "", ""
	}
	_, e := s.SetAgents(r, []string{"codex"})
	must(t, e)
	_, e = s.Select(r, []string{"review"}, true)
	must(t, e)
	location, content := find()
	if location == "" || !strings.Contains(content, "one") {
		t.Fatal("native compatibility discovery failed")
	}
	raw, e := os.ReadFile(filepath.Join(filepath.Dir(location), "resources", "data.txt"))
	must(t, e)
	if string(raw) != "one" {
		t.Fatal("resource path differs from native location")
	}
	skill(t, filepath.Dir(src), "review", "two")
	_, e = s.Refresh([]string{"review"})
	must(t, e)
	location, content = find()
	if !strings.Contains(content, "two") {
		t.Fatal("native restart missed refresh")
	}
	raw, e = os.ReadFile(filepath.Join(filepath.Dir(location), "resources", "data.txt"))
	must(t, e)
	if string(raw) != "two" {
		t.Fatal("native resource missed refresh")
	}
	_, e = s.Select(r, []string{"review"}, false)
	must(t, e)
	location, _ = find()
	if location != "" {
		t.Fatal("native discovery retained removed fixture", location)
	}
}

// A skill can be linked into some of a repository's harnesses and not others.
// The narrower setting is the skill's own; the repository's harness list and
// every other skill are left alone.
func TestPerSkillHarnessSelection(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "work")
	must(t, err2(s.Select(r, []string{"review"}, true)))

	codex := filepath.Join(r, AgentPath("codex"), "review")
	claude := filepath.Join(r, AgentPath("claude"), "review")
	checkLink(t, codex, s.mustPath(t, "review"))
	checkLink(t, claude, s.mustPath(t, "review"))

	must(t, err2(s.SelectHarnesses(r, "review", []string{"codex"})))
	checkLink(t, codex, s.mustPath(t, "review"))
	absent(t, claude)

	st, e := ReadState(r)
	must(t, e)
	if got := st.SkillAgents("review"); len(got) != 1 || got[0] != "codex" {
		t.Fatal("state does not record the narrower selection:", got, st.Harnesses)
	}
	if len(st.Agents) != 2 {
		t.Fatal("the repository's own harnesses changed:", st.Agents)
	}
	rows, e := s.Catalog(r)
	must(t, e)
	if rows[0].States["claude"] != "off" || rows[0].States["codex"] != "active" {
		t.Fatal("catalog does not report the narrower selection:", rows[0].States)
	}

	// Widening back to every enabled harness stops recording an exception.
	must(t, err2(s.SelectHarnesses(r, "review", []string{"codex", "claude"})))
	checkLink(t, claude, s.mustPath(t, "review"))
	if st, e = ReadState(r); e != nil || len(st.Harnesses) != 0 {
		t.Fatal("widening left an exception behind:", st.Harnesses, e)
	}

	// A skill linked nowhere is a skill that is off.
	must(t, err2(s.SelectHarnesses(r, "review", nil)))
	absent(t, codex)
	absent(t, claude)
	if st, e = ReadState(r); e != nil || len(st.Selected) != 0 {
		t.Fatal("unlinking every harness left the skill on:", st.Selected, e)
	}
}

// err2 drops the results of a call that only needs its error checked.
func err2(_ []Result, e error) error { return e }

// mustPath resolves a library entry's content directory.
func (s *Store) mustPath(t *testing.T, name string) string {
	t.Helper()
	p, e := s.Path(name)
	must(t, e)
	return p
}
