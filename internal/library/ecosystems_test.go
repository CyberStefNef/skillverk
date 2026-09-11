package library

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ecosystemFixture(t *testing.T) (*Store, string, string) {
	t.Helper()
	s, root, _ := fixture(t)
	home := filepath.Join(root, "home")
	must(t, os.MkdirAll(home, 0755))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("HERMES_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local/state"))
	s.ScanRoots = nil
	return s, root, home
}
func TestEcosystemDiscoveryAndNativeMigration(t *testing.T) {
	for _, owner := range []string{"cursor", "gemini", "opencode", "windsurf", "factory", "pi", "vibe", "copilot", "antigravity", "hermes"} {
		t.Run(owner, func(t *testing.T) {
			s, root, _ := ecosystemFixture(t)
			r := repo(t, root, "project")
			var relative string
			for path, agent := range ecosystemProjects {
				if agent == owner {
					relative = path
					break
				}
			}
			// Include a nested package so migration must preserve its scope.
			src := skill(t, filepath.Join(r, "packages", "web", relative), "client", "Fixture")
			_, err := gitAt(r, "add", ".")
			must(t, err)
			plan, err := s.ScanSetup(context.Background(), []string{r})
			must(t, err)
			if len(plan.Items) != 1 || plan.Items[0].Path != src || plan.Items[0].Action != "keep" || !plan.Items[0].Tracked {
				t.Fatal(plan.Items)
			}
			plan.Items[0].Action = "share"
			_, err = s.ApplySetup(context.Background(), plan, false)
			if err == nil {
				t.Fatal("unapproved migration")
			}
			if linkTarget(src) != "" {
				t.Fatal("decline changed original")
			}
			_, err = s.ApplySetup(context.Background(), plan, true)
			must(t, err)
			central, err := s.Path("client")
			must(t, err)
			if !samePath(linkTarget(src), central) {
				t.Fatal("original client path not linked", src)
			}
			if Exists(LinkPath(r, "codex", "client")) || Exists(LinkPath(r, "claude", "client")) {
				t.Fatal("nested skill leaked into repository-wide scope")
			}
			out, err := gitAt(r, "ls-files", "--", filepath.ToSlash(strings.TrimPrefix(src, r+string(filepath.Separator))))
			must(t, err)
			if out != "" {
				t.Fatal("original tracking remained", out)
			}
		})
	}
}
func TestFlatSkillImportRefreshAndNativeLink(t *testing.T) {
	s, root, home := ecosystemFixture(t)
	dir := filepath.Join(home, ".pi", "agent", "skills")
	src := filepath.Join(dir, "client.md")
	put(t, src, "---\nname: client\ndescription: Fixture\n---\nRead scripts/tool.sh")
	put(t, filepath.Join(dir, "scripts", "tool.sh"), "fixture")
	c, e := OpenCollection(src)
	must(t, e)
	defer c.Close()
	_, e = s.Publish(c, nil, false)
	must(t, e)
	central, e := s.Path("client")
	must(t, e)
	readSibling(t, filepath.Join(central, "scripts", "tool.sh"), "fixture")
	put(t, filepath.Join(dir, "scripts", "tool.sh"), "updated")
	results, e := s.Refresh([]string{"client"})
	must(t, e)
	for _, result := range results {
		if result.Error != "" {
			t.Fatal(result)
		}
	}
	readSibling(t, filepath.Join(central, "scripts", "tool.sh"), "updated")
	// Resources beside a native flat entry must remain with that installation.
	guarded, e := s.ScanSetup(context.Background(), nil)
	must(t, e)
	if len(guarded.Items) != 1 || guarded.Items[0].Action != "keep" || guarded.Items[0].ManagerIssue == "" {
		t.Fatal(guarded.Items)
	}
	put(t, src, "---\nname: client\ndescription: Fixture\n---\nStandalone instructions")
	// A self-contained flat skill can retain its native filename as a file link.
	s2, e := New(filepath.Join(root, "second"))
	must(t, e)
	plan, e := s2.ScanSetup(context.Background(), nil)
	must(t, e)
	if len(plan.Items) != 1 {
		t.Fatal(plan.Items)
	}
	_, e = s2.ApplySetup(context.Background(), plan, true)
	must(t, e)
	if info, e := os.Stat(src); e != nil || !info.Mode().IsRegular() {
		t.Fatal("native Markdown entry lost", e)
	}
	if sk, e := ReadSkill(src); e != nil || sk.Name != "client" {
		t.Fatal(sk, e)
	}
}
func TestOpenSkillsHandoff(t *testing.T) {
	s, _, home := ecosystemFixture(t)
	src := skill(t, filepath.Join(home, ".claude", "skills"), "client", "Fixture")
	metadata := filepath.Join(src, ".openskills.json")
	before := `{"sourceType":"github","repoUrl":"https://github.com/example/skills.git","subpath":"skills/client","installedAt":"fixture"}`
	put(t, metadata, before)
	p, e := s.ScanSetup(context.Background(), nil)
	must(t, e)
	if len(p.Items) != 1 || len(p.Items[0].Managers) != 1 {
		t.Fatal(p.Items)
	}
	_, e = s.ApplySetup(context.Background(), p, true)
	must(t, e)
	var detached map[string]any
	b, e := os.ReadFile(metadata)
	must(t, e)
	must(t, json.Unmarshal(b, &detached))
	if detached["managedBy"] != "skillverk" || detached["repoUrl"] != nil {
		t.Fatal(detached)
	}
	rows, e := s.Catalog("")
	must(t, e)
	if rows[0].Entry.Upstream != "https://github.com/example/skills.git" {
		t.Fatal(rows)
	}
}
func TestHermesAndAmpRequirements(t *testing.T) {
	for _, front := range []string{
		"mcpServers:\n  fixture:\n    command: missing-fixture-tool\n",
		"metadata:\n  hermes:\n    requires_toolsets: [terminal]\n",
		"metadata:\n  hermes:\n    config: [{key: fixture}]\n",
	} {
		s, root, _ := fixture(t)
		src := skill(t, root, "client", "Fixture")
		put(t, filepath.Join(src, "SKILL.md"), "---\nname: client\ndescription: Fixture\n"+front+"---\nFixture")
		c, e := OpenCollection(src)
		must(t, e)
		_, e = s.Publish(c, nil, false)
		c.Close()
		must(t, e)
		r := repo(t, root, "project")
		_, e = s.Select(r, []string{"client"}, true)
		if e == nil {
			t.Fatal("runtime-dependent skill activated", front)
		}
	}
}

func TestConfiguredRootsAndStaleConfiguration(t *testing.T) {
	s, root, home := ecosystemFixture(t)
	external := filepath.Join(root, "external")
	skill(t, external, "client", "Fixture")
	config := filepath.Join(home, ".hermes", "config.yaml")
	put(t, config, "skills:\n  external_dirs:\n    - "+external+"\n")
	p, e := s.ScanSetup(context.Background(), nil)
	must(t, e)
	if len(p.Items) != 1 {
		t.Fatal(p)
	}
	put(t, config, "skills:\n  external_dirs:\n    - "+external+"\n  create_dir: "+filepath.Join(root, "new")+"\n")
	_, e = s.ApplySetup(context.Background(), p, true)
	if e == nil || !strings.Contains(e.Error(), "changed") {
		t.Fatal(e)
	}
	if linkTarget(filepath.Join(external, "client")) != "" {
		t.Fatal("stale plan moved original")
	}
}
func TestHermesHubHandoffPreservesBundledAndOtherRecords(t *testing.T) {
	s, _, home := ecosystemFixture(t)
	base := filepath.Join(home, ".hermes", "skills")
	skill(t, base, "bundled", "Fixture")
	src := skill(t, filepath.Join(base, "category"), "client", "Fixture")
	put(t, filepath.Join(base, ".bundled_manifest"), "bundled:fixturehash\n")
	lock := filepath.Join(base, ".hub", "lock.json")
	put(t, lock, `{"version":1,"installed":{"client":{"source":"url","identifier":"https://example.com/client/SKILL.md","install_path":"category/client","content_hash":"fixture"},"other":{"install_path":"other"}}}`)
	p, e := s.ScanSetup(context.Background(), nil)
	must(t, e)
	if len(p.Items) != 2 {
		t.Fatal(p.Items)
	}
	for _, item := range p.Items {
		if item.Name == "bundled" && (item.Action != "keep" || item.ManagerIssue == "") {
			t.Fatal(item)
		}
		if item.Name == "client" && len(item.Managers) != 1 {
			t.Fatal(item)
		}
	}
	_, e = s.ApplySetup(context.Background(), p, true)
	must(t, e)
	if linkTarget(src) == "" {
		t.Fatal("hub skill not linked")
	}
	var doc map[string]any
	b, e := os.ReadFile(lock)
	must(t, e)
	must(t, json.Unmarshal(b, &doc))
	installed := doc["installed"].(map[string]any)
	if installed["client"] != nil || installed["other"] == nil {
		t.Fatal(doc)
	}
	readSibling(t, filepath.Join(base, ".bundled_manifest"), "bundled:fixturehash\n")
}
func TestOpenSkillsFailedImportRestoresMetadata(t *testing.T) {
	s, root, home := ecosystemFixture(t)
	src := skill(t, filepath.Join(home, ".claude", "skills"), "client", "Fixture")
	outside := filepath.Join(root, "outside")
	put(t, outside, "outside")
	must(t, os.Symlink(outside, filepath.Join(src, "escape")))
	file := filepath.Join(src, ".openskills.json")
	original := `{"sourceType":"github","repoUrl":"https://github.com/example/skills.git","subpath":"client"}`
	put(t, file, original)
	p, e := s.ScanSetup(context.Background(), nil)
	must(t, e)
	_, e = s.ApplySetup(context.Background(), p, true)
	if e == nil {
		t.Fatal("escaping source imported")
	}
	readSibling(t, file, original)
	if linkTarget(src) != "" {
		t.Fatal("original changed")
	}
}
func TestSkillArchiveAndPackageFilters(t *testing.T) {
	s, root, _ := fixture(t)
	archive := filepath.Join(root, "client.skill")
	must(t, os.WriteFile(archive, testZip(t, map[string]string{"client/SKILL.md": remoteSkill}), 0600))
	c, e := OpenCollection(archive)
	must(t, e)
	defer c.Close()
	_, e = s.Publish(c, nil, false)
	must(t, e)
	pkg := filepath.Join(root, "package")
	skill(t, filepath.Join(pkg, "skills"), "keep", "Fixture")
	skill(t, filepath.Join(pkg, "skills"), "skip", "Fixture")
	put(t, filepath.Join(pkg, "package.json"), `{"pi":{"skills":["skills","!skills/skip"]}}`)
	c2, e := OpenCollection(pkg)
	must(t, e)
	defer c2.Close()
	if len(c2.Skills) != 1 || c2.Skills[0].Name != "keep" {
		t.Fatal(c2.Skills)
	}
}

func TestFlatResourcesPreserveNestedRelativePaths(t *testing.T) {
	_, root, _ := fixture(t)
	src := filepath.Join(root, "source", "client.md")
	put(t, src, "---\ndescription: Fixture\n---\nRead docs/guide.md and config.json.")
	put(t, filepath.Join(root, "source", "config.json"), "{}")
	put(t, filepath.Join(root, "source", "docs", "guide.md"), "Read ../scripts/tool.sh and detail.md.")
	put(t, filepath.Join(root, "source", "docs", "detail.md"), "Details")
	put(t, filepath.Join(root, "source", "scripts", "tool.sh"), "fixture")
	dst := filepath.Join(root, "copy")
	must(t, copySkill(src, dst))
	for _, rel := range []string{"config.json", "docs/guide.md", "docs/detail.md", "scripts/tool.sh"} {
		if !Exists(filepath.Join(dst, rel)) {
			t.Fatal("missing", rel)
		}
	}
	put(t, src, "---\ndescription: Fixture\n---\nRead ../outside.txt.")
	if e := copySkill(src, filepath.Join(root, "escape")); e == nil {
		t.Fatal("outside resource accepted")
	}
}
func TestConfiguredJSONCAndMultilineVibe(t *testing.T) {
	_, root, home := ecosystemFixture(t)
	custom := filepath.Join(root, "config")
	t.Setenv("XDG_CONFIG_HOME", custom)
	first, second := filepath.Join(root, "one"), filepath.Join(root, "two")
	put(t, filepath.Join(custom, "opencode", "opencode.jsonc"), `{"skills":{"paths":["`+first+`",],}, // comment
}`)
	put(t, filepath.Join(home, ".vibe", "config.toml"), "skill_paths = [\n  \""+second+"\", # comment\n]\n")
	roots, issues := configuredRoots("")
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	found := map[string]bool{}
	for _, r := range roots {
		found[r.Path] = true
	}
	if !found[first] || !found[second] {
		t.Fatal(roots)
	}
}
func TestCancelledNativeDiscovery(t *testing.T) {
	_, root, _ := ecosystemFixture(t)
	r := repo(t, root, "project")
	skill(t, filepath.Join(r, "packages", "nested", ".cursor", "skills"), "client", "Fixture")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := nestedRoots(ctx, r); e != context.Canceled {
		t.Fatal(e)
	}
	if _, e := installationPaths(ctx, r); e != context.Canceled {
		t.Fatal(e)
	}
}

func TestPiGitPackageSources(t *testing.T) {
	for _, input := range []string{"git:github.com/example/skills@v1", "git:https://github.com/example/skills@v1"} {
		value, e := piGitSource(input)
		must(t, e)
		remote, ref, _, e := repositoryURL(value)
		must(t, e)
		if remote != "https://github.com/example/skills" || ref != "v1" {
			t.Fatal(remote, ref)
		}
	}
	if _, e := piGitSource("git:https://github.com/example/skills@"); e == nil {
		t.Fatal("empty ref accepted")
	}
}
func TestFlatNativeDeletion(t *testing.T) {
	s, _, home := ecosystemFixture(t)
	src := filepath.Join(home, ".pi", "agent", "skills", "client.md")
	put(t, src, "---\nname: client\ndescription: Fixture\n---\nFixture")
	p, e := s.ScanSetup(context.Background(), nil)
	must(t, e)
	_, e = s.ApplySetup(context.Background(), p, true)
	must(t, e)
	results, e := s.Delete("client", true)
	must(t, e)
	for _, result := range results {
		if result.Error != "" {
			t.Fatal(result)
		}
	}
	if _, e = os.Lstat(src); !os.IsNotExist(e) {
		t.Fatal("native alias remains", e)
	}
}

func TestPackageExplicitHiddenSkillAndAgentExclusion(t *testing.T) {
	_, root, _ := fixture(t)
	pkg := filepath.Join(root, "package")
	skill(t, filepath.Join(pkg, ".private"), "client", "Fixture")
	put(t, filepath.Join(pkg, "agents", "worker.md"), "---\nname: worker\ndescription: Agent definition\n---\nWorker")
	put(t, filepath.Join(pkg, "package.json"), `{"pi":{"skills":[".private/client"]}}`)
	c, e := OpenCollection(pkg)
	must(t, e)
	defer c.Close()
	if len(c.Skills) != 1 || c.Skills[0].Name != "client" {
		t.Fatal(c.Skills)
	}
	must(t, os.Remove(filepath.Join(pkg, "package.json")))
	if c, e := OpenCollection(pkg); e == nil {
		c.Close()
		t.Fatal("agent definition discovered as skill")
	}
}

func TestPlainNativeMarkdownDiscovery(t *testing.T) {
	s, _, home := ecosystemFixture(t)
	base := filepath.Join(home, ".gemini", "antigravity-cli", "skills")
	put(t, filepath.Join(base, "client.md"), "Review the proposed change.")
	put(t, filepath.Join(base, "README.md"), "Skill directory documentation.")
	p, e := s.ScanSetup(context.Background(), nil)
	must(t, e)
	if len(p.Items) != 1 || p.Items[0].Name != "client" {
		t.Fatal(p.Items)
	}
}

func TestHermesGitHubSourceHandoff(t *testing.T) {
	s, _, home := ecosystemFixture(t)
	base := filepath.Join(home, ".hermes", "skills")
	skill(t, base, "client", "Fixture")
	put(t, filepath.Join(base, ".hub", "lock.json"), `{"version":1,"installed":{"client":{"source":"github","identifier":"example/skills/skills/client","install_path":"client"}}}`)
	p, e := s.ScanSetup(context.Background(), nil)
	must(t, e)
	if len(p.Items) != 1 || len(p.Items[0].Managers) != 1 {
		t.Fatal(p.Items)
	}
	m := p.Items[0].Managers[0]
	if m.Source != "https://github.com/example/skills.git" || m.SkillPath != "skills/client" {
		t.Fatal(m)
	}
}
