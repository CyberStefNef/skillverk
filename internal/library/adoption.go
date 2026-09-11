package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Adopt takes an observed installation path, avoiding ambiguous same-name copies.
// Project originals move to private custody before their native path becomes a
// managed link. Custody is retained until a separately confirmed cleanup.
func (s *Store) Adopt(repo, path string, replace bool) ([]Result, error) {
	return s.adopt(repo, path, replace, false)
}

// Migrate requires consent for complete original removal, including Git tracking.
// Originals are deleted only after central content and native links are verified.
// Failures retain recoverable originals and are reported to the caller.
func (s *Store) Migrate(repo string, paths []string, replace, confirmed bool) ([]Result, error) {
	if !confirmed {
		return nil, errors.New("confirm complete removal of originals, including Git tracking, before migration")
	}
	// Migrate aliases before their repository-owned target becomes a shared link.
	paths = slices.Clone(paths)
	slices.SortStableFunc(paths, func(a, b string) int {
		ai, ae := os.Lstat(Expand(a))
		bi, be := os.Lstat(Expand(b))
		alink := ae == nil && (ai.Mode()&os.ModeSymlink != 0 || linkTarget(Expand(a)) != "")
		blink := be == nil && (bi.Mode()&os.ModeSymlink != 0 || linkTarget(Expand(b)) != "")
		if alink == blink {
			return 0
		}
		if alink {
			return -1
		}
		return 1
	})
	var results []Result
	var failures []error
	pending := map[string]error{}
	for _, path := range paths {
		abs, err := filepath.Abs(Expand(path))
		if err != nil {
			return results, err
		}
		rs, err := s.adopt(repo, abs, replace, true)
		results = append(results, rs...)
		pending[abs] = err
	}
	originals, err := s.Originals("")
	if err != nil {
		return results, err
	}
	for _, o := range originals {
		if _, selected := pending[o.Path]; !selected || (o.Scope == "project" && o.Repo != repo) {
			continue
		}
		r, err := s.CleanupOriginal(o.ID, true)
		results = append(results, r)
		if err != nil {
			failures = append(failures, err)
		} else {
			pending[o.Path] = nil
		}
	}
	for path, err := range pending {
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", path, err))
		}
	}
	// Earlier agent conflicts can be resolved by migrating another selected copy.
	// Cleanup above rechecks all enabled agents before accepting that recovery.
	if len(failures) == 0 {
		results = slices.DeleteFunc(results, func(r Result) bool { return r.Error != "" })
	}
	return results, errors.Join(failures...)
}

func (s *Store) adopt(repo, path string, replace, untrack bool) ([]Result, error) {
	path, e := filepath.Abs(Expand(path))
	if e != nil {
		return nil, e
	}
	owned, e := managerRecords(repo, path, filepath.Base(path))
	if e != nil {
		return nil, e
	}
	if len(owned) > 0 {
		return nil, errors.New("installation has another updater; use setup to review the ownership handoff")
	}
	originalSkill, e := ReadSkill(path)
	if e != nil {
		return nil, e
	}
	requirements, e := inspectRequirements(context.Background(), path, originalSkill.Name)
	if e != nil {
		return nil, e
	}
	if e = requirements.check(originalSkill.Name, path); e != nil {
		return nil, e
	}
	rows, e := s.Catalog(repo)
	if e != nil {
		return nil, e
	}
	var found *Installation
	for _, sk := range rows {
		for _, i := range sk.Installations {
			if samePath(i.Path, path) {
				x := i
				found = &x
			}
		}
	}
	if found == nil {
		return nil, errors.New("adoption requires an observed standalone installation path; run list or inspect")
	}
	if found.Scope == "plugin" || found.Scope == "inherited" {
		return nil, errors.New("plugin/inherited installations stay with their owner; adopt standalone originals in their own repository")
	}
	if found.Tracked && !untrack {
		return nil, fmt.Errorf("original is tracked by Git: %s; adoption cannot replace tracked files with locally ignored links. Resolve tracking in the repository before retrying; the original was preserved", path)
	}
	if found.Scope == "project" {
		if e = checkParents(repo, found.Owner); e != nil {
			return nil, e
		}
	}
	if found.Tracked {
		rel, er := filepath.Rel(repo, path)
		if er != nil {
			return nil, er
		}
		if _, er = gitAt(repo, "rm", "-r", "--cached", "--dry-run", "--", filepath.ToSlash(rel)); er != nil {
			return nil, er
		}
	}
	if found.Scope == "project" {
		sk, err := ReadSkill(path)
		if err != nil {
			return nil, err
		}
		if filepath.Base(path) != sk.Name {
			return nil, errors.New("installation directory must match its skill name before project adoption")
		}
	}
	before, e := digest(path)
	if e != nil {
		return nil, e
	}
	c, e := OpenCollection(path)
	if e != nil {
		return nil, e
	}
	defer c.Close()
	published, e := s.publishAdoption(c, replace)
	if e != nil {
		return nil, e
	}
	chosen, selectErr := c.Select(nil)
	if selectErr != nil {
		return nil, selectErr
	}
	sk := chosen[0]
	entry := published[sk.Name]
	u, e := s.lock()
	if e != nil {
		return nil, e
	}
	defer u()
	d, e := s.load()
	if e != nil {
		return nil, e
	}
	if d.Entries[sk.Name].ID != entry.ID {
		return nil, errors.New("library changed during adoption; retry")
	}
	hash, e := digest(path)
	if e != nil {
		return nil, e
	}
	if hash != before {
		return nil, errors.New("original changed during import; preserved, retry adoption")
	}
	centralDigest, e := digest(s.entryPath(entry))
	if e != nil {
		return nil, e
	}
	if e = s.verifySiblings(d, entry); e != nil {
		return nil, e
	}
	original := Original{ContentDigest: centralDigest, EntryID: entry.ID, ID: newID(), Name: sk.Name, Path: path, Digest: hash, Scope: found.Scope, Repo: repo}
	rs := []Result{{Name: sk.Name, Path: s.entryPath(entry), Action: "imported"}}
	if found.Scope == "global" {
		original.Repo = ""
		d.Originals = append(d.Originals, original)
		e = s.save(d)
		return append(rs, Result{Name: sk.Name, Path: path, Action: "global original retained; activation is separate; optional cleanup affects other repositories"}), e
	}
	meta, e := metadata(repo)
	if e != nil {
		return rs, e
	}
	ru, e := fileLock(filepath.Join(meta, "skillverk.lock"))
	if e != nil {
		return rs, e
	}
	defer ru()
	st, _, e := s.state(repo)
	if e != nil {
		return rs, e
	}
	// Do not replace a tracked original and claim Git is clean.
	agent := found.Owner
	if !slices.Contains(Agents, agent) {
		return rs, errors.New("unknown project destination")
	}
	if e = checkParents(repo, agent); e != nil {
		return rs, e
	}
	if filepath.Base(path) != sk.Name {
		return rs, errors.New("installation directory must match its skill name before project adoption")
	}
	original.Context = st.Context
	original.Backup = filepath.Join(meta, "skillverk-originals", original.ID, "original")
	if e = os.MkdirAll(filepath.Dir(original.Backup), 0700); e != nil {
		return rs, e
	}
	d.Originals = append(d.Originals, original)
	if e = s.save(d); e != nil {
		return rs, e
	}
	if e = os.Rename(path, original.Backup); e != nil {
		return rs, fmt.Errorf("import succeeded, original preserved; cannot move original into private custody: %w", e)
	}
	if samePath(c.Root, path) {
		entry.Source = original.Backup
	}
	entry.Subpath = "."
	d.Entries[sk.Name] = entry
	if e = s.save(d); e != nil {
		return rs, e
	}
	restoreTracking := func() error { return nil }
	if found.Tracked {
		restoreTracking, e = untrackOriginal(repo, path)
		if e != nil {
			return rs, errors.Join(e, os.Rename(original.Backup, path))
		}
		rs = append(rs, Result{Name: sk.Name, Path: path, Action: "original removal staged in Git"})
	}
	if e = exclude(repo, agent, filepath.Base(path)); e != nil {
		return rs, errors.Join(e, os.Rename(original.Backup, path), restoreTracking())
	}
	st.Selected[sk.Name] = entry.ID
	if e = writeJSON(statePath(meta), st); e != nil {
		return rs, errors.Join(e, os.Rename(original.Backup, path), restoreTracking())
	}
	more, e := s.apply(&d, repo, meta, &st)
	rs = append(rs, more...)
	// If this destination failed, restore its original availability where possible.
	if slices.Contains(st.Agents, agent) && !samePath(linkTarget(path), s.entryPath(entry)) && !Exists(path) {
		if er := os.Rename(original.Backup, path); er != nil {
			e = errors.Join(e, er)
		} else {
			d.Originals = slices.DeleteFunc(d.Originals, func(x Original) bool { return x.ID == original.ID })
			entry.Source = c.Source
			d.Entries[sk.Name] = entry
			e = errors.Join(e, s.save(d), restoreTracking())
		}
	}
	return rs, errors.Join(e, ResultsError(rs))
}
func (s *Store) Originals(name string) ([]Original, error) {
	u, e := s.lock()
	if e != nil {
		return nil, e
	}
	defer u()
	d, e := s.load()
	if e != nil {
		return nil, e
	}
	var out []Original
	for _, o := range d.Originals {
		if name == "" || o.Name == name {
			out = append(out, o)
		}
	}
	return out, nil
}
func (s *Store) CleanupOriginal(id string, confirmed bool) (Result, error) {
	r, e := s.cleanupOriginal(id, confirmed)
	if e != nil {
		r.Action = "original cleanup incomplete"
		r.Error = e.Error()
	}
	return r, e
}
func (s *Store) cleanupOriginal(id string, confirmed bool) (Result, error) {
	r := Result{Action: "original removed"}
	if !confirmed {
		return r, errors.New("show original paths and confirm cleanup first; global removal affects other repositories")
	}
	u, e := s.lock()
	if e != nil {
		return r, e
	}
	defer u()
	d, e := s.load()
	if e != nil {
		return r, e
	}
	idx := -1
	var o Original
	for i, x := range d.Originals {
		if x.ID == id {
			idx = i
			o = x
			break
		}
	}
	if idx < 0 {
		return r, errors.New("unknown original cleanup ID")
	}
	r.Name = o.Name
	r.Path = o.Path
	entry, ok := d.Entries[o.Name]
	if !ok || entry.ID != o.EntryID {
		return r, errors.New("central import no longer exists; original preserved")
	}
	if _, e = ReadSkill(s.entryPath(entry)); e != nil {
		return r, e
	}
	if e = s.verifySiblings(d, entry); e != nil {
		return r, e
	}
	if e = s.verifyRequirements(o.Name, entry); e != nil {
		return r, e
	}
	if h, er := digest(s.entryPath(entry)); er != nil || h != o.ContentDigest {
		return r, errors.New("central content changed after import; original preserved")
	}
	if o.Scope == "project" {
		st, er := ReadState(o.Repo)
		if er != nil {
			return r, er
		}
		if st.Context != o.Context || st.Selected[o.Name] != entry.ID {
			return r, errors.New("project migration is not complete; original preserved")
		}
		for _, a := range st.Agents {
			if st.Errors[key(o.Name, a)] != "" || !samePath(linkTarget(LinkPath(o.Repo, a, o.Name)), s.entryPath(entry)) {
				return r, fmt.Errorf("%s migration is incomplete; original preserved", a)
			}
		}
	}
	if o.Scope == "global" && o.Backup != "" {
		verified := false
		for _, a := range d.Links {
			if a.Repo == "" && a.ID == o.EntryID && a.Created && samePath(a.Path, o.Path) && (samePath(linkTarget(a.Path), s.entryPath(entry)) || samePath(linkTarget(a.Path), filepath.Join(s.entryPath(entry), "SKILL.md"))) {
				verified = true
			}
		}
		if !verified {
			return r, errors.New("global migration is incomplete; original preserved")
		}
	}
	path := o.Path
	if o.Backup != "" {
		path = o.Backup
	}
	actual, e := digest(path)
	if e != nil {
		return r, e
	}
	if actual != o.Digest {
		return r, errors.New("original changed after import; preserved")
	}
	if e = os.RemoveAll(path); e != nil {
		return r, e
	}
	d.Originals = slices.Delete(d.Originals, idx, idx+1)
	e = s.save(d)
	return r, e
}

// Acknowledging the offer is private to this worktree and never activates skills.
func (s *Store) AcknowledgeAdoption(repo string) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	meta, err := metadata(repo)
	if err != nil {
		return err
	}
	release, err := fileLock(filepath.Join(meta, "skillverk.lock"))
	if err != nil {
		return err
	}
	defer release()
	st, _, err := s.state(repo)
	if err != nil {
		return err
	}
	st.AdoptionReviewed = true
	return writeJSON(statePath(meta), st)
}

// Reuse identical content during adoption, including recovery from a previous
// import-only attempt. Different content still requires explicit replacement.
func (s *Store) publishAdoption(c *Collection, replace bool) (map[string]Entry, error) {
	selected, selectErr := c.Select(nil)
	if selectErr != nil {
		return nil, selectErr
	}
	entries, err := s.publish(c, nil, replace, false)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || replace || len(selected) != 1 {
		return entries, err
	}
	unlock, e := s.lock()
	if e != nil {
		return nil, e
	}
	defer unlock()
	d, e := s.load()
	if e != nil {
		return nil, e
	}
	sk := selected[0]
	entry, ok := d.Entries[sk.Name]
	if !ok {
		return nil, err
	}
	tmp, e := os.MkdirTemp(s.Root, ".adoption-compare-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(tmp)
	if e = copySkill(sk.Path, filepath.Join(tmp, "content")); e != nil {
		return nil, e
	}
	incoming, e := digest(filepath.Join(tmp, "content"))
	if e != nil {
		return nil, e
	}
	current, e := digest(s.entryPath(entry))
	if e != nil {
		return nil, e
	}
	if incoming != current {
		return nil, err
	}
	var known []string
	for name := range d.Entries {
		known = append(known, name)
	}
	entry.Siblings, e = siblingReferences(sk.Path, known...)
	if e != nil {
		return nil, e
	}
	entry.Requirements, e = inspectRequirements(context.Background(), sk.Path, sk.Name)
	if e != nil {
		return nil, e
	}
	if e = s.ensureSiblingAlias(sk.Name, entry); e != nil {
		return nil, e
	}
	if e = s.prepareSiblingAliases(d, entry); e != nil {
		return nil, e
	}
	d.Entries[sk.Name] = entry
	if e = s.save(d); e != nil {
		return nil, e
	}
	return map[string]Entry{sk.Name: entry}, nil
}

// Retain exact staged entries so a failed filesystem operation can restore the
// index without staging the user's current working copy.
func untrackOriginal(repo, path string) (func() error, error) {
	rel, e := filepath.Rel(repo, path)
	if e != nil {
		return nil, e
	}
	cmd := exec.Command("git", "-C", repo, "ls-files", "--stage", "-z", "--", filepath.ToSlash(rel))
	cmd.Env = gitEnv()
	staged, e := cmd.Output()
	if e != nil {
		return nil, e
	}
	if _, e = gitAt(repo, "rm", "-r", "--cached", "--", filepath.ToSlash(rel)); e != nil {
		return nil, e
	}
	return func() error {
		cmd := exec.Command("git", "-C", repo, "update-index", "-z", "--index-info")
		cmd.Env = gitEnv()
		cmd.Stdin = strings.NewReader(string(staged))
		out, e := cmd.CombinedOutput()
		if e != nil {
			return fmt.Errorf("restore Git tracking: %w: %s", e, Clean(string(out)))
		}
		return nil
	}, nil
}
