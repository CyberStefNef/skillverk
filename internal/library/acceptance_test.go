package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func put(t *testing.T, p, body string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(p), 0755))
	must(t, os.WriteFile(p, []byte(body), 0644))
}
func skill(t *testing.T, root, name, body string) string {
	t.Helper()
	p := filepath.Join(root, name)
	put(t, filepath.Join(p, "SKILL.md"), "---\nname: "+name+"\ndescription: A harmless test skill\n---\n"+body)
	put(t, filepath.Join(p, "resources", "data.txt"), body)
	return p
}
func repo(t *testing.T, root, name string) string {
	t.Helper()
	p := filepath.Join(root, name)
	must(t, os.MkdirAll(p, 0755))
	_, e := gitAt(p, "init", "-q")
	must(t, e)
	_, e = gitAt(p, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "initial")
	must(t, e)
	return p
}
func fixture(t *testing.T) (*Store, string, string) {
	t.Helper()
	root := tempDir(t)
	s, e := New(filepath.Join(root, "library"))
	must(t, e)
	s.ScanRoots = []ScanRoot{}
	src := skill(t, filepath.Join(root, "source"), "review", "one")
	c, e := OpenCollection(src)
	must(t, e)
	defer c.Close()
	_, e = s.Publish(c, nil, false)
	must(t, e)
	return s, root, src
}
func checkLink(t *testing.T, p, target string) {
	t.Helper()
	if !samePath(linkTarget(p), target) {
		t.Fatalf("%s points to %q, want %s", p, linkTarget(p), target)
	}
	_, e := ReadSkill(p)
	must(t, e)
}
func absent(t *testing.T, p string) {
	t.Helper()
	if Exists(p) {
		t.Fatalf("unexpected path %s", p)
	}
}
func TestSharedLibraryAcceptance(t *testing.T) {
	s, root, src := fixture(t)
	a := repo(t, root, "a")
	b := repo(t, root, "b")
	c := repo(t, root, "c")
	rows, e := s.Catalog(a)
	must(t, e)
	if len(rows) != 1 || rows[0].Selected {
		t.Fatalf("unseen repository: %+v", rows)
	}
	outside, e := Project(root)
	must(t, e)
	if outside != "" {
		t.Fatal(outside)
	}
	if _, e = s.Select("", []string{"review"}, true); e == nil {
		t.Fatal("outside activation accepted")
	}
	target, e := s.Path("review")
	must(t, e)
	for _, r := range []string{a, b} {
		_, e = s.Select(r, []string{"review"}, true)
		must(t, e)
		for _, ag := range DefaultAgents {
			checkLink(t, LinkPath(r, ag, "review"), target)
		}
		status, e := gitAt(r, "status", "--porcelain")
		must(t, e)
		if status != "" {
			t.Fatal(status)
		}
		absent(t, filepath.Join(r, ".gitignore"))
		absent(t, filepath.Join(r, ".skillverk.json"))
	}
	absent(t, LinkPath(c, "codex", "review"))
	skill(t, filepath.Dir(src), "review", "two")
	data, e := os.ReadFile(filepath.Join(target, "resources", "data.txt"))
	must(t, e)
	if string(data) != "one" {
		t.Fatal("source was not independent")
	}
	_, e = s.Refresh([]string{"review"})
	must(t, e)
	for _, r := range []string{a, b} {
		for _, ag := range DefaultAgents {
			checkLink(t, LinkPath(r, ag, "review"), target)
			data, e = os.ReadFile(filepath.Join(LinkPath(r, ag, "review"), "resources", "data.txt"))
			must(t, e)
			if string(data) != "two" {
				t.Fatal("shared refresh not visible")
			}
		}
	}
	_, e = s.Select(a, []string{"review"}, false)
	must(t, e)
	for _, ag := range DefaultAgents {
		absent(t, LinkPath(a, ag, "review"))
		checkLink(t, LinkPath(b, ag, "review"), target)
	}
	_, e = s.Delete("review", false)
	if e == nil {
		t.Fatal("unconfirmed deletion")
	}
	_, e = s.Delete("review", true)
	must(t, e)
	absent(t, target)
	for _, r := range []string{a, b, c} {
		for _, ag := range DefaultAgents {
			absent(t, LinkPath(r, ag, "review"))
		}
		st, e := ReadState(r)
		must(t, e)
		if len(st.Selected) != 0 {
			t.Fatal(st)
		}
	}
	d, e := s.load()
	must(t, e)
	if len(d.Links) != 0 {
		t.Fatal(d.Links)
	}
}
func TestNestedRepositoriesAndWorktrees(t *testing.T) {
	s, root, _ := fixture(t)
	a := repo(t, root, "a")
	sub := filepath.Join(a, "deep", "inside")
	must(t, os.MkdirAll(sub, 0755))
	got, e := Project(sub)
	must(t, e)
	if got != a {
		t.Fatal(got)
	}
	nested := repo(t, sub, "nested")
	got, e = Project(nested)
	must(t, e)
	if got != nested {
		t.Fatal(got)
	}
	work := filepath.Join(root, "worktree")
	_, e = gitAt(a, "worktree", "add", "-q", "-b", "separate", work)
	must(t, e)
	_, e = s.Select(a, []string{"review"}, true)
	must(t, e)
	st, e := ReadState(work)
	must(t, e)
	if len(st.Selected) != 0 {
		t.Fatal("worktree selection inherited")
	}
	_, e = s.SetAgents(work, []string{"claude"})
	must(t, e)
	_, e = s.Select(work, []string{"review"}, true)
	must(t, e)
	absent(t, LinkPath(work, "codex", "review"))
	aState, e := ReadState(a)
	must(t, e)
	if len(aState.Agents) != 2 {
		t.Fatal("agent setting leaked")
	}
	ma, e := metadata(a)
	must(t, e)
	mw, e := metadata(work)
	must(t, e)
	if ma == mw {
		t.Fatal("shared selection metadata")
	}
	for _, r := range []string{a, work} {
		out, e := gitAt(r, "status", "--porcelain")
		must(t, e)
		if (r == a && out != "?? deep/") || (r == work && out != "") {
			t.Fatal(out)
		}
	}
}
func TestPartialFailureInspectionAndExplicitRetry(t *testing.T) {
	s, root, _ := fixture(t)
	a := repo(t, root, "a")
	foreign := LinkPath(a, "claude", "review")
	skill(t, filepath.Dir(foreign), "review", "foreign")
	rs, e := s.Select(a, []string{"review"}, true)
	if e == nil || len(rs) != 2 {
		t.Fatalf("%+v %v", rs, e)
	}
	target, e := s.Path("review")
	must(t, e)
	checkLink(t, LinkPath(a, "codex", "review"), target)
	rows, e := s.Catalog(a)
	must(t, e)
	if rows[0].Status() != "partial" {
		t.Fatal(rows[0])
	}
	must(t, os.RemoveAll(foreign))
	_, e = s.Reconcile(a)
	must(t, e)
	rows, e = s.Catalog(a)
	must(t, e)
	absent(t, foreign)
	if rows[0].Status() != "partial" {
		t.Fatal("opening hid failure")
	}
	_, e = s.Retry(a)
	must(t, e)
	checkLink(t, foreign, target)
	_, e = s.SetAgents(a, []string{"codex"})
	must(t, e)
	absent(t, foreign)
}
func TestAgentSettingPartialFailure(t *testing.T) {
	s, root, _ := fixture(t)
	a := repo(t, root, "a")
	_, e := s.SetAgents(a, []string{"codex"})
	must(t, e)
	_, e = s.Select(a, []string{"review"}, true)
	must(t, e)
	put(t, filepath.Join(a, ".claude"), "blocked parent")
	_, e = s.SetAgents(a, Agents)
	if e == nil {
		t.Fatal("expected agent-specific error")
	}
	target, e := s.Path("review")
	must(t, e)
	checkLink(t, LinkPath(a, "codex", "review"), target)
	must(t, os.Remove(filepath.Join(a, ".claude")))
	_, e = s.Reconcile(a)
	must(t, e)
	absent(t, LinkPath(a, "claude", "review"))
	_, e = s.Retry(a)
	must(t, e)
	checkLink(t, LinkPath(a, "claude", "review"), target)
}
func TestUnreachableDeletionAndNewLifetime(t *testing.T) {
	s, root, src := fixture(t)
	a := repo(t, root, "a")
	_, e := s.Select(a, []string{"review"}, true)
	must(t, e)
	old, e := s.Path("review")
	must(t, e)
	moved := filepath.Join(root, "offline")
	must(t, os.Rename(a, moved))
	rs, e := s.Delete("review", true)
	if e == nil {
		t.Fatalf("unreachable cleanup reported complete: %+v", rs)
	}
	absent(t, old)
	d, e := s.load()
	must(t, e)
	if len(d.Links) != 2 || !d.Links[0].Pending {
		t.Fatal(d)
	}
	c, e := OpenCollection(src)
	must(t, e)
	defer c.Close()
	_, e = s.Publish(c, nil, false)
	must(t, e)
	fresh, e := s.Path("review")
	must(t, e)
	if fresh == old {
		t.Fatal("reused deletion lifetime")
	}
	// Simulate a newer unrelated link at the old recorded path.
	must(t, os.Remove(LinkPath(moved, "codex", "review")))
	must(t, directoryLink(fresh, LinkPath(moved, "codex", "review")))
	_, e = s.Reconcile(moved)
	if e == nil {
		t.Fatal("foreign replacement cleanup should be explicit")
	}
	checkLink(t, LinkPath(moved, "codex", "review"), fresh)
	absent(t, LinkPath(moved, "claude", "review"))
	st, e := ReadState(moved)
	must(t, e)
	if len(st.Selected) != 0 {
		t.Fatal("resurrected selection")
	}
	must(t, os.Remove(LinkPath(moved, "codex", "review")))
	_, e = s.Reconcile(moved)
	must(t, e)
	_, e = s.Select(moved, []string{"review"}, true)
	must(t, e)
	checkLink(t, LinkPath(moved, "codex", "review"), fresh)
}
func TestForeignReplacementPreserved(t *testing.T) {
	s, root, _ := fixture(t)
	a := repo(t, root, "a")
	_, e := s.Select(a, []string{"review"}, true)
	must(t, e)
	p := LinkPath(a, "codex", "review")
	must(t, os.Remove(p))
	skill(t, filepath.Dir(p), "review", "foreign")
	_, e = s.Delete("review", true)
	if e == nil {
		t.Fatal("foreign replacement not reported")
	}
	raw, e := os.ReadFile(filepath.Join(p, "resources", "data.txt"))
	must(t, e)
	if string(raw) != "foreign" {
		t.Fatal("foreign content deleted")
	}
	absent(t, LinkPath(a, "claude", "review"))
}
func TestCollectionConflictsAndFailedRefresh(t *testing.T) {
	s, root, src := fixture(t)
	skill(t, filepath.Dir(src), "other", "other")
	c, e := OpenCollection(filepath.Dir(src))
	must(t, e)
	defer c.Close()
	_, e = s.Publish(c, []string{"other"}, false)
	must(t, e)
	rows, e := s.Catalog("")
	must(t, e)
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	for _, sk := range rows {
		if sk.Selected {
			t.Fatal("import activated")
		}
	}
	_, e = s.Publish(c, []string{"review"}, false)
	var conflict *ConflictError
	if !errors.As(e, &conflict) || !strings.Contains(e.Error(), "Existing source:") {
		t.Fatal(e)
	}
	target, e := s.Path("review")
	must(t, e)
	must(t, os.RemoveAll(src))
	_, e = s.Refresh([]string{"review"})
	if e == nil {
		t.Fatal("missing source refresh succeeded")
	}
	_, e = ReadSkill(target)
	must(t, e)
	// Validation of an invalid refresh leaves old complete content.
	skill(t, filepath.Join(root, "source"), "review", "replacement")
	put(t, filepath.Join(src, "SKILL.md"), "invalid")
	_, e = s.Refresh([]string{"review"})
	if e == nil {
		t.Fatal("invalid refresh succeeded")
	}
	_, e = ReadSkill(target)
	must(t, e)
}
func TestProjectAndGlobalAdoption(t *testing.T) {
	root := tempDir(t)
	s, e := New(filepath.Join(root, "library"))
	must(t, e)
	s.ScanRoots = []ScanRoot{}
	a := repo(t, root, "a")
	p := skill(t, filepath.Join(a, ".claude", "skills"), "review", "project")
	rs, e := s.Adopt(a, p, false)
	must(t, e)
	if len(rs) < 3 {
		t.Fatal(rs)
	}
	target, e := s.Path("review")
	must(t, e)
	for _, ag := range DefaultAgents {
		checkLink(t, LinkPath(a, ag, "review"), target)
	}
	orig, e := s.Originals("review")
	must(t, e)
	if len(orig) != 1 {
		t.Fatal(orig)
	}
	if !Exists(orig[0].Backup) {
		t.Fatal("original not retained")
	}
	_, e = s.CleanupOriginal(orig[0].ID, false)
	if e == nil {
		t.Fatal("unconfirmed cleanup")
	}
	_, e = s.CleanupOriginal(orig[0].ID, true)
	must(t, e)
	absent(t, orig[0].Backup)
	globalRoot := filepath.Join(root, "global")
	gp := skill(t, globalRoot, "global-skill", "global")
	s.ScanRoots = []ScanRoot{{globalRoot, "global", "claude"}}
	_, e = s.Adopt(a, gp, false)
	must(t, e)
	st, e := ReadState(a)
	must(t, e)
	if st.Selected["global-skill"] != "" {
		t.Fatal("global adoption activated")
	}
	orig, e = s.Originals("global-skill")
	must(t, e)
	put(t, filepath.Join(gp, "resources", "data.txt"), "changed")
	_, e = s.CleanupOriginal(orig[0].ID, true)
	if e == nil {
		t.Fatal("changed original removed")
	}
	put(t, filepath.Join(gp, "resources", "data.txt"), "global")
	_, e = s.CleanupOriginal(orig[0].ID, true)
	must(t, e)
	absent(t, gp)
}
func TestAdoptionKeepsOriginalOnPartialMigration(t *testing.T) {
	root := tempDir(t)
	s, e := New(filepath.Join(root, "library"))
	must(t, e)
	s.ScanRoots = []ScanRoot{}
	a := repo(t, root, "a")
	p := skill(t, filepath.Join(a, ".agents", "skills"), "review", "project")
	put(t, filepath.Join(a, ".claude"), "conflict")
	_, e = s.Adopt(a, p, false)
	if e == nil {
		t.Fatal("partial migration reported success")
	}
	orig, e := s.Originals("review")
	must(t, e)
	if len(orig) != 1 {
		t.Fatal(orig)
	}
	_, e = s.CleanupOriginal(orig[0].ID, true)
	if e == nil {
		t.Fatal("partial migration original removed")
	}
	if !Exists(orig[0].Backup) {
		t.Fatal("lost original")
	}
	must(t, os.Remove(filepath.Join(a, ".claude")))
	_, e = s.Retry(a)
	must(t, e)
	_, e = s.CleanupOriginal(orig[0].ID, true)
	must(t, e)
}
func TestTrackedPathsAndIgnoreOverrides(t *testing.T) {
	for _, tracked := range []bool{true, false} {
		t.Run(map[bool]string{true: "tracked", false: "override"}[tracked], func(t *testing.T) {
			s, root, _ := fixture(t)
			a := repo(t, root, "a")
			if tracked {
				skill(t, filepath.Join(a, ".agents", "skills"), "review", "tracked")
				_, e := gitAt(a, "add", ".agents")
				must(t, e)
			} else {
				put(t, filepath.Join(a, ".gitignore"), "!.agents/skills/review\n")
			}
			_, e := s.Select(a, []string{"review"}, true)
			if e == nil {
				t.Fatal("Git visibility conflict accepted")
			}
			if !tracked {
				absent(t, LinkPath(a, "codex", "review"))
			}
		})
	}
}
func TestContentLinkSafety(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("package symlink creation needs privileges; normal directory junction tests run separately")
	}
	s, root, src := fixture(t)
	put(t, filepath.Join(root, "outside"), "secret")
	must(t, os.Symlink(filepath.Join(root, "outside"), filepath.Join(src, "escape")))
	_, e := s.Refresh([]string{"review"})
	if e == nil {
		t.Fatal("escaped resource accepted")
	}
	target, e := s.Path("review")
	must(t, e)
	absent(t, filepath.Join(target, "escape"))
	must(t, os.Remove(filepath.Join(src, "escape")))
	must(t, os.Symlink("resources/data.txt", filepath.Join(src, "local")))
	_, e = s.Refresh([]string{"review"})
	must(t, e)
	info, e := os.Lstat(filepath.Join(target, "local"))
	must(t, e)
	if !info.Mode().IsRegular() {
		t.Fatal("resource did not become independent")
	}
}
func TestConcurrentActivation(t *testing.T) {
	s, root, _ := fixture(t)
	a := repo(t, root, "a")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.Select(a, []string{"review"}, true); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		must(t, e)
	}
	d, e := s.load()
	must(t, e)
	if len(d.Links) != 2 {
		t.Fatal(d.Links)
	}
}
func TestFailedIntentIsClearedOnCentralDeletion(t *testing.T) {
	s, root, _ := fixture(t)
	a := repo(t, root, "a")
	put(t, filepath.Join(a, ".agents"), "blocked")
	put(t, filepath.Join(a, ".claude"), "blocked")
	_, e := s.Select(a, []string{"review"}, true)
	if e == nil {
		t.Fatal("expected failures")
	}
	_, _ = s.Delete("review", true)
	st, e := ReadState(a)
	must(t, e)
	if len(st.Selected) != 0 {
		t.Fatal("reachable failed selection not cleared")
	}
}

func TestRefreshInterruptionRecovery(t *testing.T) {
	s, _, _ := fixture(t)
	d, e := s.load()
	must(t, e)
	entry := d.Entries["review"]
	target := s.entryPath(entry)
	stage, e := os.MkdirTemp(s.Root, ".import-")
	must(t, e)
	tx := contentTransaction{Name: "review", Entry: entry, Previous: &entry, Stage: stage, HadContent: true}
	must(t, writeJSON(filepath.Join(s.Root, "content-transaction.json"), tx))
	must(t, os.Rename(target, filepath.Join(stage, "previous")))
	put(t, filepath.Join(target, "SKILL.md"), "incomplete")
	_, e = s.Catalog("")
	must(t, e)
	_, e = ReadSkill(target)
	must(t, e)
	absent(t, stage)
	absent(t, filepath.Join(s.Root, "content-transaction.json"))
	raw, e := os.ReadFile(filepath.Join(target, "resources", "data.txt"))
	must(t, e)
	if string(raw) != "one" {
		t.Fatal("previous complete content not restored")
	}
}
func TestToggleDoesNotRetryOtherFailedSkill(t *testing.T) {
	s, root, _ := fixture(t)
	a := repo(t, root, "a")
	put(t, filepath.Join(a, ".claude"), "blocked")
	_, e := s.Select(a, []string{"review"}, true)
	if e == nil {
		t.Fatal("expected failure")
	}
	must(t, os.Remove(filepath.Join(a, ".claude")))
	src := skill(t, filepath.Join(root, "source"), "other", "other")
	c, e := OpenCollection(src)
	must(t, e)
	defer c.Close()
	_, e = s.Publish(c, nil, false)
	must(t, e)
	_, e = s.Select(a, []string{"other"}, true)
	must(t, e)
	absent(t, LinkPath(a, "claude", "review"))
	if !Exists(LinkPath(a, "claude", "other")) {
		t.Fatal("other skill not activated")
	}
}
func TestPermissionFailureAndPendingContentCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode permissions; Windows native suite exercises link failures separately")
	}
	s, root, _ := fixture(t)
	a := repo(t, root, "a")
	p := filepath.Join(a, ".claude")
	must(t, os.Mkdir(p, 0500))
	defer os.Chmod(p, 0755)
	// Skip this fixture only when the process bypasses Unix directory permissions.
	f, e := os.Create(filepath.Join(p, "permission-probe"))
	if e == nil {
		f.Close()
		os.Remove(f.Name())
		t.Skip("process bypasses Unix permissions")
	}
	_, e = s.Select(a, []string{"review"}, true)
	if e == nil {
		t.Fatal("permission failure reported as success")
	}
	target, e := s.Path("review")
	must(t, e)
	checkLink(t, LinkPath(a, "codex", "review"), target)
	must(t, os.Chmod(p, 0755))
	_, e = s.Retry(a)
	must(t, e)
	resources := filepath.Join(target, "resources")
	must(t, os.Chmod(resources, 0000))
	defer os.Chmod(resources, 0755)
	_, e = s.Delete("review", true)
	if e == nil {
		t.Fatal("inaccessible content cleanup reported complete")
	}
	d, e := s.load()
	must(t, e)
	if len(d.Removing) != 1 {
		t.Fatal("lost pending central cleanup")
	}
	must(t, os.Chmod(resources, 0755))
	_, e = s.Reconcile("")
	must(t, e)
	absent(t, target)
}
func TestGitCollectionAcquisitionAndRefresh(t *testing.T) {
	root := tempDir(t)
	remote := repo(t, root, "remote")
	skill(t, remote, "git-skill", "original")
	_, e := gitAt(remote, "add", ".")
	must(t, e)
	_, e = gitAt(remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "skill")
	must(t, e)
	// Git's URL rewrite keeps this acquisition test deterministic and offline,
	// while still exercising clone, checkout, Git metadata and future refetch.
	localURL := "file://" + filepath.ToSlash(remote)
	if runtime.GOOS == "windows" {
		localURL = "file:///" + filepath.ToSlash(remote)
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+localURL+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://fixture.invalid/skills.git")
	s, e := New(filepath.Join(root, "library"))
	must(t, e)
	s.ScanRoots = []ScanRoot{}
	c, e := OpenCollection("https://fixture.invalid/skills.git")
	must(t, e)
	if c.Commit == "" {
		t.Fatal("Git checkout missing acquisition evidence")
	}
	_, e = s.Publish(c, []string{"git-skill"}, false)
	must(t, e)
	c.Close()
	target, e := s.Path("git-skill")
	must(t, e)
	skill(t, remote, "git-skill", "refreshed")
	_, e = gitAt(remote, "add", ".")
	must(t, e)
	_, e = gitAt(remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "refresh")
	must(t, e)
	_, e = s.Refresh([]string{"git-skill"})
	must(t, e)
	raw, e := os.ReadFile(filepath.Join(target, "resources", "data.txt"))
	must(t, e)
	if string(raw) != "refreshed" {
		t.Fatal("Git refresh did not refetch")
	}
}

func TestLinkedParentPreserved(t *testing.T) {
	s, root, _ := fixture(t)
	a := repo(t, root, "a")
	global := filepath.Join(root, "external")
	skill(t, filepath.Join(global, "skills"), "review", "foreign")
	must(t, directoryLink(global, filepath.Join(a, ".claude")))
	_, e := s.Select(a, []string{"review"}, true)
	if e == nil {
		t.Fatal("linked parent accepted")
	}
	target, e := s.Path("review")
	must(t, e)
	checkLink(t, LinkPath(a, "codex", "review"), target)
	_, _ = s.Delete("review", true)
	raw, e := os.ReadFile(filepath.Join(global, "skills", "review", "resources", "data.txt"))
	must(t, e)
	if string(raw) != "foreign" {
		t.Fatal("global content changed")
	}
}
func TestVisitMovedActiveRepositoryRepairsIndex(t *testing.T) {
	s, root, _ := fixture(t)
	a := repo(t, root, "a")
	_, e := s.Select(a, []string{"review"}, true)
	must(t, e)
	moved := filepath.Join(root, "moved")
	must(t, os.Rename(a, moved))
	_, e = s.Reconcile(moved)
	must(t, e)
	_, e = s.Delete("review", true)
	must(t, e)
	for _, ag := range DefaultAgents {
		absent(t, LinkPath(moved, ag, "review"))
	}
}

func TestAdoptedProjectLinkRetainsRefreshSource(t *testing.T) {
	root := tempDir(t)
	s, e := New(filepath.Join(root, "library"))
	must(t, e)
	s.ScanRoots = []ScanRoot{}
	a := repo(t, root, "a")
	src := skill(t, filepath.Join(root, "original"), "review", "one")
	path := LinkPath(a, "claude", "review")
	must(t, os.MkdirAll(filepath.Dir(path), 0755))
	must(t, directoryLink(src, path))
	_, e = s.Adopt(a, path, false)
	must(t, e)
	orig, e := s.Originals("review")
	must(t, e)
	_, e = s.CleanupOriginal(orig[0].ID, true)
	must(t, e)
	skill(t, filepath.Dir(src), "review", "two")
	_, e = s.Refresh([]string{"review"})
	must(t, e)
	raw, e := os.ReadFile(filepath.Join(path, "resources", "data.txt"))
	must(t, e)
	if string(raw) != "two" {
		t.Fatal("lost linked installation's source")
	}
}

func TestInventorySkipsBookkeepingFiles(t *testing.T) {
	s, root, _ := fixture(t)
	global := filepath.Join(root, "global")
	put(t, filepath.Join(global, ".system", ".codex-system-skills.marker"), "marker")
	put(t, filepath.Join(global, "readme.txt"), "not a skill")
	skill(t, filepath.Join(global, ".system"), "actual-skill", "valid")
	s.ScanRoots = []ScanRoot{{global, "global", "codex"}}
	rows, e := s.Catalog("")
	must(t, e)
	found := false
	for _, sk := range rows {
		if sk.Name == "actual-skill" {
			found = true
		}
		if sk.Name == ".codex-system-skills.marker" || sk.Name == "readme.txt" {
			t.Fatal("bookkeeping file inventoried as skill")
		}
	}
	if !found {
		t.Fatal("real system skill lost")
	}
}

func TestAdoptionAcknowledgementIsWorktreePrivate(t *testing.T) {
	s, root, _ := fixture(t)
	a := repo(t, root, "a")
	work := filepath.Join(root, "worktree")
	_, e := gitAt(a, "worktree", "add", "-q", "-b", "other", work)
	must(t, e)
	must(t, s.AcknowledgeAdoption(a))
	st, e := ReadState(a)
	must(t, e)
	if !st.AdoptionReviewed || len(st.Selected) != 0 {
		t.Fatal(st)
	}
	other, e := ReadState(work)
	must(t, e)
	if other.AdoptionReviewed || other.Library != "" {
		t.Fatal("dismissal leaked to other worktree")
	}
	out, e := gitAt(a, "status", "--porcelain")
	must(t, e)
	if out != "" {
		t.Fatal(out)
	}
}

func TestCompleteMigrationTrackedOriginals(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "project")
	// The central copy already exists from an earlier import-only adoption.
	a := skill(t, filepath.Join(r, ".agents", "skills"), "review", "one")
	b := skill(t, filepath.Join(r, ".claude", "skills"), "review", "one")
	_, e := gitAt(r, "add", ".agents", ".claude")
	must(t, e)
	_, e = gitAt(r, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "skills")
	must(t, e)
	before, e := s.Path("review")
	must(t, e)
	_, e = s.Migrate(r, []string{a, b}, false, false)
	if e == nil {
		t.Fatal("migration accepted without removal consent")
	}
	tracked, e := gitAt(r, "ls-files", "--", ".agents", ".claude")
	must(t, e)
	if !strings.Contains(tracked, "SKILL.md") {
		t.Fatal("decline removed tracking")
	}
	_, e = s.Adopt(r, a, false)
	if e == nil {
		t.Fatal("ordinary adoption untracked original")
	}
	_, e = s.Migrate(r, []string{a, b}, false, true)
	must(t, e)
	after, e := s.Path("review")
	must(t, e)
	if before != after {
		t.Fatal("existing library entry replaced")
	}
	checkLink(t, a, after)
	checkLink(t, b, after)
	tracked, e = gitAt(r, "ls-files", "--", ".agents", ".claude")
	must(t, e)
	if tracked != "" {
		t.Fatal("originals still tracked", tracked)
	}
	staged, e := gitAt(r, "diff", "--cached", "--name-status")
	must(t, e)
	if !strings.Contains(staged, "D\t.agents/skills/review/SKILL.md") || !strings.Contains(staged, "D\t.claude/skills/review/SKILL.md") {
		t.Fatal("missing staged removals", staged)
	}
	originals, e := s.Originals("review")
	must(t, e)
	if len(originals) != 0 {
		t.Fatal("saved originals remain", originals)
	}
}

func TestTrackedMigrationConflictPreservesOriginal(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "project")
	p := skill(t, filepath.Join(r, ".agents", "skills"), "review", "different")
	_, e := gitAt(r, "add", ".agents")
	must(t, e)
	_, e = s.Migrate(r, []string{p}, false, true)
	if e == nil {
		t.Fatal("different central content replaced silently")
	}
	tracked, e := gitAt(r, "ls-files", "--", ".agents")
	must(t, e)
	if !strings.Contains(tracked, "SKILL.md") {
		t.Fatal("conflict untracked original")
	}
	i, e := os.Lstat(p)
	must(t, e)
	if !i.IsDir() {
		t.Fatal("original replaced on conflict")
	}
}

func TestRepositoryOwnershipSurvivesSharedChanges(t *testing.T) {
	s, root, src := fixture(t)
	a := repo(t, root, "local")
	b := repo(t, root, "shared")
	_, e := s.Select(a, []string{"review"}, true)
	must(t, e)
	_, e = s.Select(b, []string{"review"}, true)
	must(t, e)
	_, e = s.Localize(a, "review", false)
	if e == nil {
		t.Fatal("localization lacked consent")
	}
	_, e = s.Localize(a, "review", true)
	must(t, e)
	local := LinkPath(a, "codex", "review")
	i, e := os.Lstat(local)
	must(t, e)
	if !i.IsDir() {
		t.Fatal("local content is still linked")
	}
	target, e := os.Readlink(LinkPath(a, "claude", "review"))
	must(t, e)
	if filepath.IsAbs(target) {
		t.Fatal("repository link must be portable")
	}
	st, e := ReadState(a)
	must(t, e)
	if len(st.Selected) != 0 {
		t.Fatal("still follows shared updates")
	}
	tracked, e := gitAt(a, "ls-files", "--", ".agents/skills/review/SKILL.md", ".claude/skills/review")
	must(t, e)
	if !strings.Contains(tracked, "SKILL.md") || !strings.Contains(tracked, ".claude/skills/review") {
		t.Fatal("local content not staged", tracked)
	}
	sk, e := s.Find(a, "review")
	must(t, e)
	if sk.Ownership() != "repository" || sk.Storage != "repository" {
		t.Fatal("wrong ownership", sk)
	}
	put(t, filepath.Join(src, "resources", "data.txt"), "updated")
	_, e = s.Refresh([]string{"review"})
	must(t, e)
	raw, e := os.ReadFile(filepath.Join(local, "resources", "data.txt"))
	must(t, e)
	if string(raw) != "one" {
		t.Fatal("shared update changed local content")
	}
	_, e = s.Delete("review", true)
	must(t, e)
	_, e = ReadSkill(local)
	must(t, e)
	_, e = ReadSkill(LinkPath(a, "claude", "review"))
	must(t, e)
	absent(t, LinkPath(b, "codex", "review"))
}

func TestLocalizePreservesConflictAndIndex(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "project")
	original := skill(t, filepath.Join(r, ".claude", "skills"), "review", "local")
	_, e := s.Localize(r, "review", true)
	if e == nil {
		t.Fatal("overwrote repository content")
	}
	raw, e := os.ReadFile(filepath.Join(original, "resources", "data.txt"))
	must(t, e)
	if string(raw) != "local" {
		t.Fatal("original changed")
	}
	absent(t, LinkPath(r, "codex", "review"))
	staged, e := gitAt(r, "diff", "--cached", "--name-only")
	must(t, e)
	if staged != "" {
		t.Fatal("conflict changed index")
	}
}

func TestRepositoryCopyCanReturnToSharedLibrary(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "project")
	_, e := s.Localize(r, "review", true)
	must(t, e)
	_, e = gitAt(r, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "local skill")
	must(t, e)
	_, e = s.Migrate(r, []string{LinkPath(r, "codex", "review"), LinkPath(r, "claude", "review")}, false, true)
	must(t, e)
	originals, e := s.Originals("review")
	must(t, e)
	if len(originals) != 0 {
		t.Fatal("migration left originals")
	}
	p, e := s.Path("review")
	must(t, e)
	checkLink(t, LinkPath(r, "codex", "review"), p)
	checkLink(t, LinkPath(r, "claude", "review"), p)
}

func TestLocalizeIndexFailureRestoresManagedLinks(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "project")
	_, e := s.Select(r, []string{"review"}, true)
	must(t, e)
	meta, e := metadata(r)
	must(t, e)
	put(t, filepath.Join(meta, "index.lock"), "locked")
	_, e = s.Localize(r, "review", true)
	if e == nil {
		t.Fatal("index failure hidden")
	}
	central, e := s.Path("review")
	must(t, e)
	for _, a := range DefaultAgents {
		checkLink(t, LinkPath(r, a, "review"), central)
	}
	st, e := ReadState(r)
	must(t, e)
	if st.Selected["review"] == "" {
		t.Fatal("lost shared selection")
	}
}

func TestCollectionSkipsInvalidSiblingSkills(t *testing.T) {
	s, root, _ := fixture(t)
	source := filepath.Join(root, "pstack")
	skill(t, filepath.Join(source, "skills"), "unslop", "valid")
	invalid := skill(t, filepath.Join(source, "skills"), "make-bot-ui", "invalid")
	put(t, filepath.Join(invalid, "SKILL.md"), "---\nname: Make Bot UI\ndescription: Invalid display name\n---\n")
	c, e := OpenCollection(source)
	must(t, e)
	defer c.Close()
	if len(c.Skills) != 1 || c.Skills[0].Name != "unslop" || len(c.Skipped) != 1 {
		t.Fatal("valid sibling blocked or invalid sibling included", c)
	}
	if c.Skipped[0].Path != "skills/make-bot-ui" || !strings.Contains(c.Skipped[0].Error, "Make Bot UI") {
		t.Fatal("missing actionable skipped entry", c.Skipped)
	}
	_, e = s.Publish(c, []string{"unslop"}, false)
	must(t, e)
	if _, e = s.Path("unslop"); e != nil {
		t.Fatal(e)
	}
	if _, e = OpenCollection(invalid); e == nil {
		t.Fatal("explicit invalid skill silently accepted")
	}
}

func TestCollectionWithoutValidSkillsReportsErrors(t *testing.T) {
	p := filepath.Join(tempDir(t), "collection")
	put(t, filepath.Join(p, "bad", "SKILL.md"), "invalid")
	if _, e := OpenCollection(p); e == nil || !strings.Contains(e.Error(), "no valid skills") {
		t.Fatal("empty broken source lacks useful error", e)
	}
}

func TestSparseSourceChecksOutOnlyRequestedSubtree(t *testing.T) {
	root := tempDir(t)
	remote := repo(t, root, "remote")
	skill(t, filepath.Join(remote, "pstack", "skills"), "unslop", "wanted")
	skill(t, filepath.Join(remote, "other-plugin", "skills"), "unrelated", "not wanted")
	_, e := gitAt(remote, "add", ".")
	must(t, e)
	_, e = gitAt(remote, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "source")
	must(t, e)
	_, e = gitAt(remote, "branch", "-M", "main")
	must(t, e)
	localURL := "file://" + filepath.ToSlash(remote)
	if runtime.GOOS == "windows" {
		localURL = "file:///" + filepath.ToSlash(remote)
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+localURL+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/fixture/skills.git")
	c, e := OpenCollection("https://github.com/fixture/skills/tree/main/pstack")
	must(t, e)
	defer c.Close()
	if len(c.Skills) != 1 || c.Skills[0].Name != "unslop" || c.Commit == "" {
		t.Fatal("wrong subtree", c)
	}
	absent(t, filepath.Join(filepath.Dir(c.Root), "other-plugin"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = OpenCollectionContext(ctx, remote); !errors.Is(e, context.Canceled) {
		t.Fatal("cancel ignored", e)
	}
}
