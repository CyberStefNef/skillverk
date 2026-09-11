package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportIncludesSiblingClosureAndDoctorDetectsDamage(t *testing.T) {
	s, root, a, b := siblingFixture(t)
	c, e := OpenCollection(filepath.Dir(a))
	must(t, e)
	defer c.Close()
	entries, e := s.Publish(c, []string{"client"}, false)
	must(t, e)
	if len(entries) != 2 {
		t.Fatal(entries)
	}
	r := repo(t, root, "work")
	_, e = s.Select(r, []string{"client"}, true)
	must(t, e)
	absent(t, LinkPath(r, "codex", "guide"))
	must(t, os.Remove(filepath.Join(s.entryPath(entries["guide"]), "docs", "start.md")))
	if _, e = s.Doctor(r); e == nil {
		t.Fatal("doctor accepted broken dependency")
	}
	if _, e = s.Retry(r); e == nil {
		t.Fatal("retry accepted broken dependency")
	}
	r2 := repo(t, root, "other")
	if _, e = s.Select(r2, []string{"client"}, true); e == nil {
		t.Fatal("activation accepted broken dependency")
	}
	absent(t, LinkPath(r2, "codex", "client"))
	if _, e = s.Refresh([]string{"client"}); e == nil {
		t.Fatal("refresh accepted missing dependency resource")
	}
	readSibling(t, filepath.Join(b, "docs", "start.md"), "first")
}
func TestSparseSiblingReferenceRejected(t *testing.T) {
	s, root, _ := fixture(t)
	src := skill(t, filepath.Join(root, "sparse"), "client", "Read [missing](../missing/SKILL.md)")
	c, e := OpenCollection(src)
	if e == nil {
		defer c.Close()
		if _, e = s.Publish(c, nil, false); e == nil {
			t.Fatal("missing sibling ignored")
		}
	}
	if _, e = s.Path("client"); e == nil {
		t.Fatal("partial import")
	}
}
func TestFixedGlobalPathMigrationAndFreshActivation(t *testing.T) {
	s, root, _ := fixture(t)
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	src := skill(t, filepath.Join(home, ".codex", "skills"), "client", "Run $CODEX_HOME/skills/client/scripts/run.py")
	put(t, filepath.Join(src, "scripts", "run.py"), "print('fixture')")
	s.ScanRoots = []ScanRoot{{filepath.Dir(src), "global", "codex"}}
	c, e := OpenCollection(src)
	must(t, e)
	defer c.Close()
	_, e = s.Publish(c, nil, false)
	must(t, e)
	r := repo(t, root, "work")
	if _, e = s.Select(r, []string{"client"}, true); e == nil || !strings.Contains(e.Error(), "global resource") {
		t.Fatal(e)
	}
	p, e := s.ScanSetup(context.Background(), nil)
	must(t, e)
	if len(p.Items) != 1 || p.Items[0].Action != "share" {
		t.Fatal(p.Items)
	}
	_, e = s.ApplySetup(context.Background(), p, true)
	must(t, e)
	_, e = s.Select(r, []string{"client"}, true)
	must(t, e)
	_, e = s.Doctor(r)
	must(t, e)
}
func TestSetupPreservesUnsupportedRuntimeRequirements(t *testing.T) {
	for _, body := range []string{"Run ~/.claude/skills/client/scripts/run.sh", "Run ${CLAUDE_PLUGIN_ROOT}/hooks/start.sh"} {
		t.Run(body, func(t *testing.T) {
			s, root, _ := fixture(t)
			t.Setenv("HOME", filepath.Join(root, "home"))
			r := repo(t, root, "work")
			src := skill(t, filepath.Join(r, ".agents", "skills"), "client", body)
			put(t, filepath.Join(src, "scripts", "run.sh"), "fixture")
			if strings.Contains(body, "CLAUDE_PLUGIN_ROOT") {
				put(t, filepath.Join(r, ".claude-plugin", "plugin.json"), `{"name":"fixture"}`)
				put(t, filepath.Join(r, "hooks", "start.sh"), "fixture")
			}
			_, e := gitAt(r, "add", ".agents/skills/client")
			must(t, e)
			indexBefore, e := gitAt(r, "ls-files", "--stage")
			must(t, e)
			p, e := s.ScanSetup(context.Background(), []string{r})
			must(t, e)
			if len(p.Items) != 1 || p.Items[0].Action != "keep" || !strings.Contains(p.Items[0].Reason, "requires") {
				t.Fatal(p.Items)
			}
			p.Items[0].Action = "share"
			if _, e = s.ApplySetup(context.Background(), p, true); e == nil {
				t.Fatal("unsupported migration allowed")
			}
			if _, e = s.Adopt(r, src, false); e == nil {
				t.Fatal("adoption bypass allowed")
			}
			indexAfter, e := gitAt(r, "ls-files", "--stage")
			must(t, e)
			if indexBefore != indexAfter {
				t.Fatal("Git tracking changed")
			}
			if linkTarget(src) != "" {
				t.Fatal("original changed")
			}
			if _, e = s.Path("client"); e == nil {
				t.Fatal("unsupported migration imported")
			}
		})
	}
}
func TestNamespaceRequirements(t *testing.T) {
	s, root, _ := fixture(t)
	dir := filepath.Join(root, "plugin")
	skill(t, dir, "client", "Use toolkit:guide.")
	skill(t, dir, "guide", "Guide")
	c, e := OpenCollection(dir)
	must(t, e)
	defer c.Close()
	entries, e := s.Publish(c, []string{"client", "guide"}, false)
	must(t, e)
	if len(entries["client"].Requirements.Plugins) != 1 {
		t.Fatal(entries)
	}
	r := repo(t, root, "work")
	if _, e = s.Select(r, []string{"client"}, true); e == nil {
		t.Fatal("namespace treated as standalone")
	}
	if _, e = s.Doctor(r); e == nil {
		t.Fatal("doctor omitted plugin dependency")
	}
}

func TestDiscoverGitHubAndCodexCollections(t *testing.T) {
	_, root, _ := fixture(t)
	source := filepath.Join(root, "collection")
	skill(t, filepath.Join(source, ".github", "skills"), "github-client", "Portable")
	skill(t, filepath.Join(source, ".codex", "skills"), "codex-client", "Portable")
	skills, _, e := discoverContext(context.Background(), source)
	must(t, e)
	if len(skills) != 2 {
		t.Fatal(skills)
	}
}

func TestRequirementsIgnoreLabelsAndOutputDirectories(t *testing.T) {
	_, root, _ := fixture(t)
	src := skill(t, root, "client", "Use labels like `bug:triage`. Write reports to ~/.claude/skills/client/output/")
	skill(t, root, "triage", "A guide")
	r, e := inspectRequirements(context.Background(), src, "client")
	must(t, e)
	if len(r.Plugins)+len(r.GlobalPaths) != 0 {
		t.Fatal(r)
	}
}

func TestRefreshPreservesActiveContentWhenPluginRequirementAppears(t *testing.T) {
	s, root, src := fixture(t)
	r := repo(t, root, "work")
	_, e := s.Select(r, []string{"review"}, true)
	must(t, e)
	central, e := s.Path("review")
	must(t, e)
	before, e := os.ReadFile(filepath.Join(central, "SKILL.md"))
	must(t, e)
	put(t, filepath.Join(filepath.Dir(src), ".claude-plugin", "plugin.json"), `{"name":"fixture"}`)
	put(t, filepath.Join(filepath.Dir(src), "scripts", "run.sh"), "fixture")
	skill(t, filepath.Dir(src), "review", "Run ${CLAUDE_PLUGIN_ROOT}/scripts/run.sh")
	if _, e = s.Refresh([]string{"review"}); e == nil {
		t.Fatal("incompatible update accepted")
	}
	after, e := os.ReadFile(filepath.Join(central, "SKILL.md"))
	must(t, e)
	if string(before) != string(after) {
		t.Fatal("active content changed")
	}
}
