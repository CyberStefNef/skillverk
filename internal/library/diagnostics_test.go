package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorReportsBrokenInstallationsWithSharedName(t *testing.T) {
	s, root, _ := fixture(t)
	for _, owner := range []string{"codex", "claude"} {
		dir := filepath.Join(root, owner)
		must(t, os.MkdirAll(dir, 0755))
		must(t, directoryLink(filepath.Join(root, "missing"), filepath.Join(dir, "review")))
		s.ScanRoots = append(s.ScanRoots, ScanRoot{dir, "global", owner})
	}
	results, err := s.Doctor("")
	if err == nil {
		t.Fatal("broken installations hidden by healthy shared skill")
	}
	for _, owner := range []string{"codex", "claude"} {
		path := filepath.Join(root, owner, "review")
		found := false
		for _, r := range results {
			if strings.Contains(r.Error, path) {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing diagnostic for %s: %+v", path, results)
		}
	}
}

func TestRefreshMissingSourcePreservesActiveContent(t *testing.T) {
	s, root, src := fixture(t)
	r := repo(t, root, "repo")
	_, err := s.Select(r, []string{"review"}, true)
	must(t, err)
	central, err := s.Path("review")
	must(t, err)
	before, err := digest(central)
	must(t, err)
	must(t, os.RemoveAll(src))
	results, err := s.Refresh([]string{"review"})
	if err == nil || len(results) != 1 || !strings.Contains(results[0].Error, "refresh source no longer exists") || !strings.Contains(results[0].Error, "--replace") {
		t.Fatalf("missing recovery guidance: %+v, %v", results, err)
	}
	after, err := digest(central)
	must(t, err)
	if after != before {
		t.Fatal("failed refresh changed central content")
	}
	for _, agent := range DefaultAgents {
		checkLink(t, LinkPath(r, agent, "review"), central)
	}
}

func TestDoctorReportsMissingOrReplacedGlobalLink(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "replaced"}[replacement], func(t *testing.T) {
			s, root, _ := fixture(t)
			global := skill(t, filepath.Join(root, "global"), "shared", "content")
			s.ScanRoots = []ScanRoot{{filepath.Dir(global), "global", "codex"}}
			_, err := s.ShareGlobal(global, false)
			must(t, err)
			_, err = s.Doctor("")
			must(t, err)
			must(t, os.Remove(global))
			if replacement {
				skill(t, filepath.Dir(global), "shared", "replacement")
			}
			results, err := s.Doctor("")
			if err == nil {
				t.Fatal("global link damage went unreported")
			}
			found := false
			for _, result := range results {
				if result.Path == global && result.Error != "" {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing global path diagnostic: %+v", results)
			}
			if replacement {
				raw, err := os.ReadFile(filepath.Join(global, "resources", "data.txt"))
				must(t, err)
				if string(raw) != "replacement" {
					t.Fatal("doctor altered replacement")
				}
			} else {
				absent(t, global)
			}
		})
	}
}

func TestDoctorSkipsSkillRootBelowRegularFile(t *testing.T) {
	s, root, _ := fixture(t)
	r := repo(t, root, "repo")
	marker := filepath.Join(r, ".codex")
	put(t, marker, "existing marker")
	s.ScanRoots = []ScanRoot{{filepath.Join(marker, "skills"), "project", "codex"}}
	results, err := s.Doctor(r)
	if err != nil {
		t.Fatalf("absent skill root reported as damage: %+v, %v", results, err)
	}
	raw, err := os.ReadFile(marker)
	must(t, err)
	if string(raw) != "existing marker" {
		t.Fatal("scanner changed existing file")
	}
}

// Compare answers whether the library copy still matches its source without
// changing either side, and survives the normalisation an import performs.
func TestCompareReportsDriftWithoutChangingAnything(t *testing.T) {
	s, _, src := fixture(t)

	drift, e := s.Compare(context.Background(), "review")
	must(t, e)
	if !drift.Same {
		t.Fatal("a freshly imported skill is reported as different from its source")
	}

	// Directory permissions the import normalises must not read as drift: the
	// copy settles directories to 0755, so a source that differs only there is
	// still the same skill.
	must(t, os.Chmod(filepath.Join(src, "resources"), 0700))
	drift, e = s.Compare(context.Background(), "review")
	must(t, e)
	if !drift.Same {
		t.Fatal("a permission difference the import normalises was reported as drift")
	}

	// Real content change, the case this exists for.
	put(t, filepath.Join(src, "resources", "data.txt"), "two")
	drift, e = s.Compare(context.Background(), "review")
	must(t, e)
	if drift.Same {
		t.Fatal("a changed source was reported as matching")
	}

	// Nothing was written: the library copy is untouched until Refresh runs.
	sk, e := s.Catalog("")
	must(t, e)
	if len(sk) != 1 {
		t.Fatal("compare disturbed the catalog:", sk)
	}
	body, e := os.ReadFile(filepath.Join(sk[0].Path, "resources", "data.txt"))
	must(t, e)
	if string(body) != "one" {
		t.Fatal("compare modified the library copy:", string(body))
	}
	if _, e = s.Compare(context.Background(), "nonexistent"); e == nil {
		t.Fatal("comparing an unknown skill succeeded")
	}
}

// The baseline is what lets a comparison say which side moved. Without it a
// difference is only a difference; with it, "out of date" and "edited here" are
// opposite situations that deserve opposite advice.
func TestCompareTellsAnOldCopyFromAnEditedOne(t *testing.T) {
	check := func(t *testing.T, s *Store, want string) {
		t.Helper()
		d, e := s.Compare(context.Background(), "review")
		must(t, e)
		if got := d.Verdict(); got != want {
			t.Fatalf("verdict %q, want %q (%+v)", got, want, d)
		}
	}

	t.Run("source moves on", func(t *testing.T) {
		s, _, src := fixture(t)
		check(t, s, "up to date")
		put(t, filepath.Join(src, "resources", "data.txt"), "upstream moved")
		check(t, s, "out of date")
	})

	t.Run("copy edited here", func(t *testing.T) {
		s, _, _ := fixture(t)
		central, e := s.Path("review")
		must(t, e)
		put(t, filepath.Join(central, "resources", "data.txt"), "my own edit")
		check(t, s, "edited here")
	})

	t.Run("both moved", func(t *testing.T) {
		s, _, src := fixture(t)
		central, e := s.Path("review")
		must(t, e)
		put(t, filepath.Join(central, "resources", "data.txt"), "my own edit")
		put(t, filepath.Join(src, "resources", "data.txt"), "upstream moved")
		check(t, s, "diverged")
	})

	// An entry written before baselines were kept can only report a difference.
	t.Run("no baseline recorded", func(t *testing.T) {
		s, _, src := fixture(t)
		unlock, e := s.lock()
		must(t, e)
		d, e := s.load()
		must(t, e)
		entry := d.Entries["review"]
		entry.Digest = ""
		d.Entries["review"] = entry
		must(t, s.save(d))
		unlock()
		put(t, filepath.Join(src, "resources", "data.txt"), "upstream moved")
		check(t, s, "different")
	})
}

// An adopted skill has no source. Finding one again means searching the sources
// the library already follows and reporting how much each match proves.
func TestFindUpstreamRecoversALostSource(t *testing.T) {
	s, root, src := fixture(t)

	// A second source that carries the same name, so there is something to find.
	other := filepath.Join(root, "other")
	skill(t, other, "review", "one")

	// Make the entry look adopted: no reachable source of its own.
	unlock, e := s.lock()
	must(t, e)
	d, e := s.load()
	must(t, e)
	entry := d.Entries["review"]
	entry.Source = filepath.Join(root, "gone", ".git", "skillverk-originals", "abc", "original")
	d.Entries["review"] = entry
	must(t, s.save(d))
	unlock()

	if _, e = s.Compare(context.Background(), "review"); e == nil {
		t.Fatal("an adopted skill claimed to have a source")
	}

	// Nothing to find until a source in the library points somewhere with it.
	found, e := s.FindUpstream(context.Background(), "review")
	must(t, e)
	if len(found) != 0 {
		t.Fatal("a local-only library found remote candidates:", found)
	}

	// Give the library a second entry whose source is a searchable location.
	c, e := OpenCollection(filepath.Join(other, "review"))
	must(t, e)
	defer c.Close()
	unlock, e = s.lock()
	must(t, e)
	d, e = s.load()
	must(t, e)
	companion := d.Entries["review"]
	companion.Source, companion.Subpath = filepath.Join(other, "review"), "."
	d.Entries["companion"] = companion
	must(t, s.save(d))
	unlock()

	// Sources only reports remote locations; a local directory is not one.
	sources, e := s.Sources()
	must(t, e)
	if len(sources) != 0 {
		t.Fatal("a local path was offered as a searchable source:", sources)
	}

	// Setting an upstream is what makes checking possible again.
	must(t, s.SetUpstream("review", src, "."))
	drift, e := s.Compare(context.Background(), "review")
	must(t, e)
	if !drift.Same {
		t.Fatal("a skill given its source back still reports drift:", drift)
	}
	if e = s.SetUpstream("review", "", ""); e == nil {
		t.Fatal("an empty upstream was accepted")
	}
	if e = s.SetUpstream("nonexistent", src, "."); e == nil {
		t.Fatal("an upstream was set on a skill that is not installed")
	}
}
