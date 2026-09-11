package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func siblingFixture(t *testing.T) (*Store, string, string, string) {
	s, root, _ := fixture(t)
	source := filepath.Join(root, "collection")
	a := skill(t, source, "client", "Read [guide](../guide/docs/start.md).")
	b := skill(t, source, "guide", "Shared reference.")
	put(t, filepath.Join(b, "docs", "start.md"), "first")
	return s, root, a, b
}
func readSibling(t *testing.T, path string, want string) {
	t.Helper()
	body, e := os.ReadFile(path)
	must(t, e)
	if string(body) != want {
		t.Fatalf("got %s, want %s", body, want)
	}
}
func TestSharedSiblingReferencesAcrossAgentsAndUpdates(t *testing.T) {
	s, root, a, b := siblingFixture(t)
	c, e := OpenCollection(filepath.Dir(a))
	must(t, e)
	defer c.Close()
	_, e = s.Publish(c, []string{"client", "guide"}, false)
	must(t, e)
	r := repo(t, root, "project")
	_, e = s.Select(r, []string{"client"}, true)
	must(t, e)
	central, e := s.Path("client")
	must(t, e)
	// Concatenation deliberately retains .. so the OS resolves it after the link.
	for _, path := range []string{central, LinkPath(r, "codex", "client"), LinkPath(r, "claude", "client")} {
		readSibling(t, path+"/../guide/docs/start.md", "first")
	}
	absent(t, LinkPath(r, "codex", "guide"))
	put(t, filepath.Join(b, "docs", "start.md"), "updated")
	_, e = s.Refresh([]string{"guide"})
	must(t, e)
	readSibling(t, LinkPath(r, "claude", "client")+"/../guide/docs/start.md", "updated")
	if _, e = s.Delete("guide", true); e == nil || !strings.Contains(e.Error(), "client") {
		t.Fatal("dependent deletion allowed", e)
	}
	_, e = s.Delete("client", true)
	must(t, e)
	absent(t, s.siblingPath("client"))
	_, e = s.Delete("guide", true)
	must(t, e)
	absent(t, s.siblingPath("guide"))
}
func TestSetupMigratesSiblingCollectionBeforeRemovingOriginals(t *testing.T) {
	s, root, a, b := siblingFixture(t)
	s.ScanRoots = []ScanRoot{{filepath.Dir(a), "global", "codex"}}
	p, e := s.ScanSetup(context.Background(), []string{root})
	must(t, e)
	_, e = s.ApplySetup(context.Background(), p, true)
	must(t, e)
	if linkTarget(a) == "" || linkTarget(b) == "" {
		t.Fatal("collection not migrated")
	}
	readSibling(t, a+"/../guide/docs/start.md", "first")
	originals, e := s.Originals("")
	must(t, e)
	if len(originals) != 0 {
		t.Fatal(originals)
	}
}
func TestSetupRequiresSiblingBeforeOriginalRemoval(t *testing.T) {
	s, root, a, _ := siblingFixture(t)
	s.ScanRoots = []ScanRoot{{filepath.Dir(a), "global", "codex"}}
	p, e := s.ScanSetup(context.Background(), []string{root})
	must(t, e)
	for i := range p.Items {
		if p.Items[i].Name == "guide" {
			p.Items[i].Action = "keep"
		}
	}
	if _, e = s.ApplySetup(context.Background(), p, true); e == nil || !strings.Contains(e.Error(), "guide") {
		t.Fatal("missing sibling accepted", e)
	}
	if linkTarget(a) != "" {
		t.Fatal("original changed")
	}
	if _, e = s.Path("client"); e == nil {
		t.Fatal("incomplete dependency plan imported")
	}
}
func TestSiblingAliasConflictPreservesOriginals(t *testing.T) {
	s, root, a, b := siblingFixture(t)
	s.ScanRoots = []ScanRoot{{filepath.Dir(a), "global", "codex"}}
	put(t, s.siblingPath("guide"), "foreign")
	p, e := s.ScanSetup(context.Background(), []string{root})
	must(t, e)
	if _, e = s.ApplySetup(context.Background(), p, true); e == nil {
		t.Fatal("foreign sibling path overwritten")
	}
	if linkTarget(a) != "" || linkTarget(b) != "" {
		t.Fatal("original removed before complete import")
	}
	readSibling(t, s.siblingPath("guide"), "foreign")
}
func TestSiblingImportFailureAndRefreshRecovery(t *testing.T) {
	s, _, a, b := siblingFixture(t)
	c, e := OpenCollection(filepath.Dir(a))
	must(t, e)
	defer c.Close()
	_, e = s.Publish(c, []string{"client", "guide"}, false)
	must(t, e)
	must(t, os.Remove(filepath.Join(b, "docs", "start.md")))
	// Refreshing the dependent with a missing source reference must preserve content.
	if _, e = s.Refresh([]string{"client"}); e == nil {
		t.Fatal("broken dependency accepted")
	}
	central, e := s.Path("client")
	must(t, e)
	readSibling(t, central+"/../guide/docs/start.md", "first")
}

func TestUpdateCannotRemoveResourceUsedBySibling(t *testing.T) {
	s, _, a, b := siblingFixture(t)
	c, e := OpenCollection(filepath.Dir(a))
	must(t, e)
	defer c.Close()
	_, e = s.Publish(c, []string{"client", "guide"}, false)
	must(t, e)
	must(t, os.Remove(filepath.Join(b, "docs", "start.md")))
	if _, e = s.Refresh([]string{"guide"}); e == nil || !strings.Contains(e.Error(), "client") {
		t.Fatal("update removed a required resource", e)
	}
	central, e := s.Path("client")
	must(t, e)
	readSibling(t, central+"/../guide/docs/start.md", "first")
}
func TestSiblingCollectionCyclesAndDeletion(t *testing.T) {
	s, root, a, b := siblingFixture(t)
	put(t, filepath.Join(b, "SKILL.md"), "---\nname: guide\ndescription: Reference\n---\n[Client](../client/SKILL.md)\n")
	s.ScanRoots = []ScanRoot{{filepath.Dir(a), "global", "codex"}}
	p, e := s.ScanSetup(context.Background(), []string{root})
	must(t, e)
	_, e = s.ApplySetup(context.Background(), p, true)
	must(t, e)
	readSibling(t, a+"/../guide/docs/start.md", "first")
	if _, e = os.ReadFile(b + "/../client/SKILL.md"); e != nil {
		t.Fatal(e)
	}
	_, e = s.DeleteMany([]string{"client", "guide"}, true)
	must(t, e)
	absent(t, a)
	absent(t, b)
}

func TestInterruptedImportRestoresSiblingIndex(t *testing.T) {
	s, _, _ := fixture(t)
	d, e := s.load()
	must(t, e)
	entry := Entry{ID: newID()}
	name := "interrupted"
	stage, e := os.MkdirTemp(s.Root, ".import-")
	must(t, e)
	must(t, os.MkdirAll(s.entryPath(entry), 0755))
	must(t, s.ensureSiblingAlias(name, entry))
	d.Entries[name] = entry
	must(t, s.save(d))
	must(t, writeJSON(filepath.Join(s.Root, "content-transaction.json"), contentTransaction{Name: name, Entry: entry, Stage: stage}))
	_, e = s.load()
	must(t, e)
	absent(t, s.entryPath(entry))
	absent(t, s.siblingPath(name))
	absent(t, stage)
}
func TestSiblingScanCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := siblingReferencesContext(ctx, tempDir(t)); e != context.Canceled {
		t.Fatal(e)
	}
}
