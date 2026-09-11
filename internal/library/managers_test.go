package library

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func managedFixture(t *testing.T) (*Store, string, string, string) {
	s, root, _ := fixture(t)
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	r := repo(t, root, "project")
	src := skill(t, filepath.Join(r, ".agents", "skills"), "client", "Fixture")
	lock := filepath.Join(r, "skills-lock.json")
	put(t, lock, `{"version":1,"custom":"preserve","skills":{"client":{"source":"example/skills","sourceType":"github","skillPath":"skills/client/SKILL.md","computedHash":"fixture"},"other":{"source":"preserve"}}}`)
	_, e := gitAt(r, "add", ".")
	must(t, e)
	return s, r, src, lock
}
func TestManagerHandoffApprovalAndTracking(t *testing.T) {
	s, r, src, lock := managedFixture(t)
	before, e := os.ReadFile(lock)
	must(t, e)
	index, e := gitAt(r, "show", ":skills-lock.json")
	must(t, e)
	p, e := s.ScanSetup(context.Background(), []string{r})
	must(t, e)
	if len(p.Items) != 1 || len(p.Items[0].Managers) != 1 || !p.Items[0].Managers[0].Tracked {
		t.Fatal(p.Items)
	}
	p.Items[0].Action = "share"
	if !strings.Contains(p.Items[0].ManagerPreview(), lock) {
		t.Fatal("missing preview")
	}
	if _, e = s.ApplySetup(context.Background(), p, false); e == nil {
		t.Fatal("approval bypass")
	}
	after, e := os.ReadFile(lock)
	must(t, e)
	if string(after) != string(before) {
		t.Fatal("decline changed lock")
	}
	if _, e = s.Adopt(r, src, false); e == nil {
		t.Fatal("direct adoption bypassed handoff")
	}
	_, e = s.ApplySetup(context.Background(), p, true)
	must(t, e)
	_, doc, skills, e := managerDocument(lock)
	must(t, e)
	if skills["client"] != nil || skills["other"] == nil || string(doc["custom"]) != `"preserve"` {
		t.Fatal(doc)
	}
	afterIndex, e := gitAt(r, "show", ":skills-lock.json")
	must(t, e)
	if index != afterIndex {
		t.Fatal("installer record was staged or untracked")
	}
	if linkTarget(src) == "" {
		t.Fatal("source not migrated")
	}
	absent(t, filepath.Join(s.Root, "manager-transfer.json"))
	entry, e := s.Find(r, "client")
	must(t, e)
	if entry.Entry.Upstream != "https://github.com/example/skills.git" || entry.Entry.UpstreamPath != "skills/client" {
		t.Fatal(entry.Entry)
	}
}
func TestManagerHandoffRejectsStaleRecord(t *testing.T) {
	s, r, src, lock := managedFixture(t)
	p, e := s.ScanSetup(context.Background(), []string{r})
	must(t, e)
	p.Items[0].Action = "share"
	put(t, lock, `{"version":1,"skills":{}}`)
	if _, e = s.ApplySetup(context.Background(), p, true); e == nil {
		t.Fatal("stale manager record accepted")
	}
	if linkTarget(src) != "" {
		t.Fatal("source changed")
	}
}
func TestManagerHandoffFailureAndRecovery(t *testing.T) {
	s, r, src, lock := managedFixture(t)
	before, e := os.ReadFile(lock)
	must(t, e)
	put(t, filepath.Join(r, "outside"), "outside")
	must(t, os.Symlink(filepath.Join(r, "outside"), filepath.Join(src, "escape")))
	p, e := s.ScanSetup(context.Background(), []string{r})
	must(t, e)
	p.Items[0].Action = "share"
	if _, e = s.ApplySetup(context.Background(), p, true); e == nil {
		t.Fatal("escaping import accepted")
	}
	after, e := os.ReadFile(lock)
	must(t, e)
	if string(before) != string(after) {
		t.Fatal("failed import lost manager record")
	}
	if linkTarget(src) != "" {
		t.Fatal("original moved")
	}
	journal, e := s.beginManagerTransfer(p.Items)
	must(t, e)
	if journal == "" {
		t.Fatal("no recovery record")
	}
	_, e = s.ScanSetup(context.Background(), []string{r})
	must(t, e)
	after, e = os.ReadFile(lock)
	must(t, e)
	if string(before) != string(after) {
		t.Fatal("interrupted handoff was not restored")
	}
}
func TestManagerRecoveryPreservesExternalEdits(t *testing.T) {
	s, r, _, lock := managedFixture(t)
	p, e := s.ScanSetup(context.Background(), []string{r})
	must(t, e)
	p.Items[0].Action = "share"
	_, e = s.beginManagerTransfer(p.Items)
	must(t, e)
	body := `{"version":1,"skills":{},"external":true}`
	put(t, lock, body)
	if e = s.finishManagerTransfer(false); e == nil {
		t.Fatal("overwrote external changes")
	}
	after, e := os.ReadFile(lock)
	must(t, e)
	if string(after) != body {
		t.Fatal("external changes lost")
	}
}
func TestClawHubPinnedHandoffRecord(t *testing.T) {
	s, root, _ := fixture(t)
	_ = s
	r := repo(t, root, "work")
	src := skill(t, filepath.Join(r, "skills"), "client", "Fixture")
	lock := filepath.Join(r, ".clawhub", "lock.json")
	put(t, lock, `{"version":1,"skills":{"client":{"version":"1.2.3","pinned":true,"ownerHandle":"fixture","installedAt":1}}}`)
	records, e := managerRecords(r, src, "client")
	must(t, e)
	if len(records) != 1 || !records[0].Pinned || records[0].Source != "https://clawhub.ai/fixture/skills/client?version=1.2.3" {
		b, _ := json.Marshal(records)
		t.Fatal(string(b))
	}
}

func TestSetupLockWaitingCanBeCanceled(t *testing.T) {
	s, _, _ := fixture(t)
	release, err := s.setupLock(context.Background())
	must(t, err)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		unlock, err := s.setupLock(ctx)
		if unlock != nil {
			unlock()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != context.DeadlineExceeded {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("setup lock ignored cancellation")
	}
}

func TestClawHubOriginPreservesRegistryAndOwner(t *testing.T) {
	_, root, _ := fixture(t)
	src := skill(t, filepath.Join(root, "skills"), "client", "Fixture")
	put(t, filepath.Join(root, ".clawhub", "lock.json"), `{"version":1,"skills":{"client":{"version":"1.2.3","pinned":true}}}`)
	put(t, filepath.Join(src, ".clawhub", "origin.json"), `{"version":1,"registry":"https://registry.example","slug":"client","ownerHandle":"fixture"}`)
	records, err := managerRecords("", src, "client")
	must(t, err)
	if len(records) != 1 || records[0].Source != "https://registry.example/api/v1/download?ownerHandle=fixture&slug=client&version=1.2.3" {
		t.Fatal(records)
	}
}
