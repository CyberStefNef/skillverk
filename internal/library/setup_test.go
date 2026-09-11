package library

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupPreviewApprovalAndRepositoryPreservation(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "project")
	local := skill(t, filepath.Join(r, ".agents", "skills"), "project-workflow", "Repository workflow")
	_, e := gitAt(r, "add", ".")
	must(t, e)
	global := skill(t, filepath.Join(root, "global"), "shared", "Reusable")
	s.ScanRoots = []ScanRoot{{filepath.Dir(global), "global", "codex"}}
	before, e := gitAt(r, "ls-files", "--stage")
	must(t, e)
	p, e := s.ScanSetup(context.Background(), []string{r})
	must(t, e)
	again, e := s.ScanSetup(context.Background(), []string{r})
	must(t, e)
	a, _ := json.Marshal(p)
	b, _ := json.Marshal(again)
	if string(a) != string(b) {
		t.Fatal("scan is not deterministic")
	}
	if len(p.Items) != 2 {
		t.Fatalf("items: %+v", p.Items)
	}
	for _, i := range p.Items {
		if i.Path == local && i.Action != "keep" {
			t.Fatal("repository default changed")
		}
	}
	if _, e = s.ApplySetup(context.Background(), p, false); e == nil {
		t.Fatal("unapproved apply accepted")
	}
	if _, e = s.Path("shared"); e == nil {
		t.Fatal("preview imported")
	}
	_, e = s.ApplySetup(context.Background(), p, true)
	must(t, e)
	central, e := s.Path("shared")
	must(t, e)
	checkLink(t, global, central)
	if linkTarget(local) != "" {
		t.Fatal("repository original replaced")
	}
	after, e := gitAt(r, "ls-files", "--stage")
	must(t, e)
	if before != after {
		t.Fatal("repository tracking changed")
	}
	st, e := ReadState(r)
	must(t, e)
	if len(st.Selected) != 0 {
		t.Fatal("global migration activated repository")
	}
	originals, e := s.Originals("")
	must(t, e)
	if len(originals) != 0 {
		t.Fatalf("left originals: %+v", originals)
	}
	_, e = s.Delete("shared", true)
	must(t, e)
	absent(t, global)
}

func TestSetupRejectsStalePlanBeforeAnyMigration(t *testing.T) {
	for _, change := range []string{"source", "central", "index", "selection", "new-installation", "identity", "symlink-target"} {
		t.Run(change, func(t *testing.T) {
			s, root, _ := fixture(t)
			r := repo(t, root, "repo")
			pth := skill(t, filepath.Join(root, "global"), "shared", "before")
			s.ScanRoots = []ScanRoot{{filepath.Dir(pth), "global", "codex"}}
			target := ""
			if change == "symlink-target" {
				target = skill(t, filepath.Join(root, "target"), "linked", "before")
				must(t, os.Symlink(target, filepath.Join(filepath.Dir(pth), "linked")))
			}
			p, e := s.ScanSetup(context.Background(), []string{r})
			must(t, e)
			switch change {
			case "source":
				put(t, filepath.Join(pth, "SKILL.md"), "changed")
			case "central":
			case "index":
				put(t, filepath.Join(r, "new"), "staged")
				_, e = gitAt(r, "add", "new")
				must(t, e)
			case "selection":
				_, e = s.SetAgents(r, []string{"codex"})
				must(t, e)
			case "new-installation":
				skill(t, filepath.Dir(pth), "new", "new")
			case "identity":
				p.Items[0].Scope = "project"
			case "symlink-target":
				put(t, filepath.Join(target, "resources", "data.txt"), "changed")
			}
			if change == "central" {
				path, e := s.Path("review")
				must(t, e)
				put(t, filepath.Join(path, "resources", "data.txt"), "changed")
			}
			if _, e = s.ApplySetup(context.Background(), p, true); e == nil {
				t.Fatal("stale plan accepted")
			}
			if linkTarget(pth) != "" {
				t.Fatal("changed source was migrated")
			}
			if _, e = s.Path("shared"); e == nil {
				t.Fatal("import before validation")
			}
		})
	}
}

func TestSetupConflictingVersionsRequireReview(t *testing.T) {
	s, root, _ := fixture(t)
	a := skill(t, filepath.Join(root, "one"), "shared", "one")
	b := skill(t, filepath.Join(root, "two"), "shared", "two")
	s.ScanRoots = []ScanRoot{{filepath.Dir(a), "global", "codex"}, {filepath.Dir(b), "global", "claude"}}
	p, e := s.ScanSetup(context.Background(), nil)
	must(t, e)
	if len(p.Items) != 2 || p.Items[0].Conflict == "" {
		t.Fatal("conflict not shown")
	}
	if _, e = s.ApplySetup(context.Background(), p, true); e == nil {
		t.Fatal("conflicting versions migrated")
	}
	for _, path := range []string{a, b} {
		if linkTarget(path) != "" {
			t.Fatal("original replaced")
		}
	}
	p.Items[1].Action = "keep"
	_, e = s.ApplySetup(context.Background(), p, true)
	must(t, e)
	p, e = s.ScanSetup(context.Background(), nil)
	must(t, e)
	if _, e = s.ApplySetup(context.Background(), p, true); e == nil {
		t.Fatal("central conflict overwritten")
	}
	p.Items[0].Action = "replace-shared"
	_, e = s.ApplySetup(context.Background(), p, true)
	must(t, e)
}

func TestSetupCancellationAndBrokenLinks(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "repo")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.ScanSetup(ctx, []string{r}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	broken := filepath.Join(r, ".agents", "skills", "broken")
	must(t, os.MkdirAll(filepath.Dir(broken), 0755))
	must(t, os.Symlink("missing", broken))
	_, e := gitAt(r, "add", ".")
	must(t, e)
	p, e := s.ScanSetup(context.Background(), []string{r})
	must(t, e)
	if len(p.Items) != 1 || p.Items[0].Action != "remove-broken" {
		t.Fatalf("%+v", p.Items)
	}
	if _, e = s.ApplySetup(ctx, p, true); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e = os.Lstat(broken); e != nil {
		t.Fatal("cancel deleted original")
	}
	_, e = s.ApplySetup(context.Background(), p, true)
	must(t, e)
	absent(t, broken)
	tracked, e := gitAt(r, "ls-files")
	must(t, e)
	if strings.Contains(tracked, "broken") {
		t.Fatal("broken link still tracked")
	}
}

func TestSetupFailedMigrationRetainsOriginal(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "repo")
	local := skill(t, filepath.Join(r, ".agents", "skills"), "shared", "local")
	foreign := skill(t, filepath.Join(r, ".claude", "skills"), "shared", "different")
	p, e := s.ScanSetup(context.Background(), []string{r})
	must(t, e)
	for n := range p.Items {
		if p.Items[n].Path == local {
			p.Items[n].Action = "share"
		}
	}
	if _, e = s.ApplySetup(context.Background(), p, true); e == nil {
		t.Fatal("expected partial migration")
	}
	if linkTarget(foreign) != "" {
		t.Fatal("foreign copy changed")
	}
	originals, e := s.Originals("shared")
	must(t, e)
	if len(originals) != 1 {
		t.Fatalf("missing retained original: %+v", originals)
	}
	if _, e = ReadSkill(originals[0].Backup); e != nil {
		t.Fatal("original content lost", e)
	}
	if _, e = s.CleanupOriginal(originals[0].ID, true); e == nil {
		t.Fatal("incomplete migration cleaned up")
	}
}

func TestSetupGlobalLinkForeignReplacementPreserved(t *testing.T) {
	s, root, _ := fixture(t)
	global := skill(t, filepath.Join(root, "global"), "shared", "content")
	s.ScanRoots = []ScanRoot{{filepath.Dir(global), "global", "codex"}}
	_, e := s.ShareGlobal(global, false)
	must(t, e)
	must(t, os.Remove(global))
	skill(t, filepath.Dir(global), "shared", "foreign")
	if _, e = s.Delete("shared", true); e == nil {
		t.Fatal("foreign replacement reported removed")
	}
	if _, e = ReadSkill(global); e != nil {
		t.Fatal("foreign replacement deleted")
	}
	if _, e = s.Reconcile(""); e == nil {
		t.Fatal("pending global cleanup silently resolved")
	}
}
func TestCleanupPreservesOriginalAfterCentralChange(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "repo")
	local := skill(t, filepath.Join(r, ".agents", "skills"), "shared", "before")
	_, e := s.Adopt(r, local, false)
	must(t, e)
	originals, e := s.Originals("shared")
	must(t, e)
	central, e := s.Path("shared")
	must(t, e)
	put(t, filepath.Join(central, "resources", "data.txt"), "changed")
	if _, e = s.CleanupOriginal(originals[0].ID, true); e == nil {
		t.Fatal("changed central copy allowed cleanup")
	}
	if _, e = ReadSkill(originals[0].Backup); e != nil {
		t.Fatal("original lost")
	}
}
func TestUntrackRecoveryPreservesStagedContent(t *testing.T) {
	_, root, _ := fixture(t)
	r := repo(t, root, "repo")
	path := filepath.Join(r, "file")
	put(t, path, "staged")
	_, e := gitAt(r, "add", "file")
	must(t, e)
	_, e = gitAt(r, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "staged")
	must(t, e)
	put(t, path, "working")
	before, e := gitAt(r, "ls-files", "--stage")
	must(t, e)
	restore, e := untrackOriginal(r, path)
	must(t, e)
	must(t, restore())
	after, e := gitAt(r, "ls-files", "--stage")
	must(t, e)
	if before != after {
		t.Fatal("staged content changed during recovery")
	}
}

func TestSetupExplicitProjectSharing(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "repo")
	a := skill(t, filepath.Join(r, ".agents", "skills"), "shared", "same")
	b := skill(t, filepath.Join(r, ".claude", "skills"), "shared", "same")
	_, e := gitAt(r, "add", ".")
	must(t, e)
	p, e := s.ScanSetup(context.Background(), []string{r})
	must(t, e)
	for n := range p.Items {
		p.Items[n].Action = "share"
	}
	_, e = s.ApplySetup(context.Background(), p, true)
	must(t, e)
	central, e := s.Path("shared")
	must(t, e)
	checkLink(t, a, central)
	checkLink(t, b, central)
	tracked, e := gitAt(r, "ls-files")
	must(t, e)
	if tracked != "" {
		t.Fatal("approved tracking removal incomplete", tracked)
	}
}

func TestSetupSkipsUnavailableRepository(t *testing.T) {
	for _, kind := range []string{"empty-git", "broken-worktree", "invalid-state"} {
		t.Run(kind, func(t *testing.T) {
			s, root, _ := fixture(t)
			good := repo(t, root, "good")
			local := skill(t, filepath.Join(good, ".agents", "skills"), "local", "keep")
			bad := filepath.Join(root, "backmatter")
			switch kind {
			case "empty-git":
				must(t, os.MkdirAll(filepath.Join(bad, ".git"), 0755))
			case "broken-worktree":
				put(t, filepath.Join(bad, ".git"), "gitdir: /missing/skillverk-test-worktree\n")
			case "invalid-state":
				bad = repo(t, root, "backmatter")
				meta, e := metadata(bad)
				must(t, e)
				put(t, statePath(meta), "invalid json")
			}
			preserved := skill(t, filepath.Join(bad, ".agents", "skills"), "unavailable", "preserve")
			global := skill(t, filepath.Join(root, "global"), "shared", "global")
			s.ScanRoots = []ScanRoot{{filepath.Dir(global), "global", "codex"}}
			p, e := s.ScanSetup(context.Background(), []string{root})
			must(t, e)
			if len(p.Repositories) != 1 || p.Repositories[0] != good {
				t.Fatalf("unavailable repository included: %v", p.Repositories)
			}
			if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], bad) {
				t.Fatalf("missing warning: %v", p.Warnings)
			}
			if len(p.Items) != 2 {
				t.Fatalf("healthy skills missing: %+v", p.Items)
			}
			again, e := s.ScanSetup(context.Background(), []string{root})
			must(t, e)
			a, _ := json.Marshal(p)
			b, _ := json.Marshal(again)
			if string(a) != string(b) {
				t.Fatal("warning scan is not deterministic")
			}
			_, e = s.ApplySetup(context.Background(), p, true)
			must(t, e)
			if _, e = ReadSkill(preserved); e != nil {
				t.Fatal("skipped original changed", e)
			}
			if linkTarget(local) != "" {
				t.Fatal("local repository changed")
			}
			central, e := s.Path("shared")
			must(t, e)
			checkLink(t, global, central)
		})
	}
}

func TestSetupRejectsPlanWhenSkippedRepositoryReturns(t *testing.T) {
	s, root, _ := fixture(t)
	bad := filepath.Join(root, "backmatter")
	must(t, os.MkdirAll(filepath.Join(bad, ".git"), 0755))
	p, e := s.ScanSetup(context.Background(), []string{root})
	must(t, e)
	_, e = gitAt(bad, "init", "-q")
	must(t, e)
	if _, e = s.ApplySetup(context.Background(), p, true); e == nil {
		t.Fatal("newly available repository did not invalidate plan")
	}
}

func TestBrokenLinkIsNotAVersionConflict(t *testing.T) {
	s, root, _ := fixture(t)
	global := filepath.Join(root, "global")
	must(t, os.MkdirAll(global, 0755))
	must(t, os.Symlink(filepath.Join(root, "missing"), filepath.Join(global, "review")))
	s.ScanRoots = []ScanRoot{{global, "global", "claude"}}
	p, e := s.ScanSetup(context.Background(), nil)
	must(t, e)
	if len(p.Items) != 1 || p.Items[0].Action != "remove-broken" || p.Items[0].Conflict != "" {
		t.Fatalf("broken link reported as conflicting content: %+v", p.Items)
	}
}
