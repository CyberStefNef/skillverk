package library

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

func checkParents(repo, agent string) error {
	p := repo
	for _, c := range strings.Split(agentPaths[agent], "/") {
		p = filepath.Join(p, c)
		i, e := os.Lstat(p)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || linkTarget(p) != "" {
			return fmt.Errorf("preserved non-directory or linked parent %s", p)
		}
	}
	return nil
}
func exclude(repo, agent, name string) error {
	return excludeNative(repo, agentPaths[agent]+"/"+name)
}
func excludeNative(repo, rel string) error {
	out, e := gitAt(repo, "ls-files", "--", rel)
	if e != nil {
		return e
	}
	if out != "" {
		return fmt.Errorf("tracked native path %s; local excludes cannot untrack it", rel)
	}
	path, e := gitAt(repo, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	unlock, e := fileLock(path + ".skillverk-lock")
	if e != nil {
		return e
	}
	defer unlock()
	raw, e := os.ReadFile(path)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	rule := "/" + rel
	if !slices.Contains(strings.Split(string(raw), "\n"), rule) {
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			raw = append(raw, '\n')
		}
		raw = append(raw, []byte(rule+"\n")...)
		if e = atomicWrite(path, raw, 0600); e != nil {
			return e
		}
	}
	cmd := exec.Command("git", "-C", repo, "check-ignore", "-q", "--", rel)
	cmd.Env = gitEnv()
	if e = cmd.Run(); e != nil {
		return fmt.Errorf("native path %s is not locally ignored (repository ignore rules may override info/exclude)", rel)
	}
	return nil
}
func findRecord(d *database, st State, repo, name, agent string) int {
	for i, a := range d.Links {
		if !a.Pending && a.Repo == repo && a.Context == st.Context && a.Name == name && a.Agent == agent {
			return i
		}
	}
	return -1
}
func (s *Store) Select(repo string, names []string, on bool) ([]Result, error) {
	return s.change(repo, func(st *State, d *database) error {
		for _, n := range names {
			if e := ValidateName(n); e != nil {
				return e
			}
			if on {
				entry, ok := d.Entries[n]
				if !ok {
					return fmt.Errorf("%s is not installed", n)
				}
				if e := s.verifySiblings(*d, entry); e != nil {
					return e
				}
				if e := s.verifyRequirements(n, entry); e != nil {
					return e
				}
				st.Selected[n] = entry.ID
				delete(st.Harnesses, n)
			} else {
				delete(st.Selected, n)
				delete(st.Harnesses, n)
			}
		}
		return nil
	}, names)
}

// SelectHarnesses narrows one skill to a subset of the enabled harnesses.
// Passing every enabled harness widens it back to the default, and passing none
// turns the skill off, because a skill linked nowhere is a skill that is off.
func (s *Store) SelectHarnesses(repo, name string, agents []string) ([]Result, error) {
	return s.change(repo, func(st *State, d *database) error {
		if e := ValidateName(name); e != nil {
			return e
		}
		var chosen []string
		for _, agent := range st.Agents {
			if slices.Contains(agents, agent) {
				chosen = append(chosen, agent)
			}
		}
		if len(chosen) == 0 {
			delete(st.Selected, name)
			delete(st.Harnesses, name)
			return nil
		}
		entry, ok := d.Entries[name]
		if !ok {
			return fmt.Errorf("%s is not installed", name)
		}
		if e := s.verifySiblings(*d, entry); e != nil {
			return e
		}
		if e := s.verifyRequirements(name, entry); e != nil {
			return e
		}
		st.Selected[name] = entry.ID
		if len(chosen) == len(st.Agents) {
			delete(st.Harnesses, name)
			return nil
		}
		if st.Harnesses == nil {
			st.Harnesses = map[string][]string{}
		}
		st.Harnesses[name] = chosen
		return nil
	}, []string{name})
}

func (s *Store) SetAgents(repo string, agents []string) ([]Result, error) {
	if len(agents) == 0 {
		return nil, errors.New("choose at least one repository harness")
	}
	for _, a := range agents {
		if !slices.Contains(Agents, a) {
			return nil, fmt.Errorf("unknown agent %s", a)
		}
	}
	normalized := []string{}
	for _, agent := range Agents {
		if slices.Contains(agents, agent) {
			normalized = append(normalized, agent)
		}
	}
	return s.change(repo, func(st *State, _ *database) error { st.Agents = normalized; return nil }, nil)
}
func (s *Store) Retry(repo string) ([]Result, error) {
	return s.change(repo, func(_ *State, _ *database) error { return nil }, nil)
}
func (s *Store) change(repo string, edit func(*State, *database) error, only []string) ([]Result, error) {
	unlock, e := s.lock()
	if e != nil {
		return nil, e
	}
	defer unlock()
	meta, e := metadata(repo)
	if e != nil {
		return nil, e
	}
	ru, e := fileLock(filepath.Join(meta, "skillverk.lock"))
	if e != nil {
		return nil, e
	}
	defer ru()
	st, _, e := s.state(repo)
	if e != nil {
		return nil, e
	}
	d, e := s.load()
	if e != nil {
		return nil, e
	}
	pending, e := s.reconcileLocked(&d, repo, &st, meta)
	if e != nil {
		return pending, e
	}
	if e = edit(&st, &d); e != nil {
		return pending, e
	}
	// Persist intent before touching native paths. Merely opening never applies it.
	if e = writeJSON(statePath(meta), st); e != nil {
		return pending, e
	}
	rs, e := s.applyOnly(&d, repo, meta, &st, only)
	rs = append(pending, rs...)
	return rs, errors.Join(e, ResultsError(rs))
}
func (s *Store) apply(d *database, repo, meta string, st *State) ([]Result, error) {
	return s.applyOnly(d, repo, meta, st, nil)
}

// intent is the desired native state for one skill/harness pair. The record
// index is resolved during the pass rather than planned up front, because
// removing an earlier pair's entry shifts every later index in d.Links.
type intent struct {
	name, agent, id, path string
	wanted                bool
	record                int
}

// affectedSkills lists the skills this pass may change, in stable order:
// everything currently selected, plus everything still recorded for this repo.
func affectedSkills(d *database, repo string, st *State, only []string) []string {
	names := map[string]bool{}
	for name := range st.Selected {
		names[name] = true
	}
	for _, a := range d.Links {
		if !a.Pending && a.Context == st.Context && a.Repo == repo {
			names[a.Name] = true
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		if only != nil && !slices.Contains(only, name) {
			continue
		}
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)
	return ordered
}
func (s *Store) applyOnly(d *database, repo, meta string, st *State, only []string) ([]Result, error) {
	var results []Result
	for _, name := range affectedSkills(d, repo, st, only) {
		for _, agent := range Agents {
			id, selected := st.Selected[name]
			t := intent{
				name:   name,
				agent:  agent,
				id:     id,
				path:   LinkPath(repo, agent, name),
				wanted: selected && st.Links(name, agent),
				record: findRecord(d, *st, repo, name, agent),
			}
			adopted := false
			if t.wanted && t.record < 0 {
				t.record, adopted = adoptGlobalAlias(d, repo, meta, st, t)
			}
			// An unselected harness that was never linked has nothing to do.
			if !t.wanted && t.record < 0 && !slices.Contains(DefaultAgents, agent) {
				continue
			}
			r, changed, e := s.reconcileLink(d, repo, meta, st, t)
			changed = changed || adopted
			results = append(results, r)
			if e != nil {
				return results, e
			}
			if changed {
				if e := s.persist(d, meta, st); e != nil {
					return results, e
				}
			}
		}
	}
	return results, nil
}

// reconcileLink brings one skill/harness pair to its intended state and records
// the outcome. It reports whether the library index or worktree state changed,
// so an unchanged pair costs no disk writes.
func (s *Store) reconcileLink(d *database, repo, meta string, st *State, t intent) (Result, bool, error) {
	r := Result{Name: t.name, Agent: t.agent, Path: t.path, Action: "inactive"}
	var changed bool
	var fatal error
	switch {
	case t.wanted:
		r.Action = "active"
		r.Error, changed, fatal = s.link(d, repo, meta, st, t)
	case t.record >= 0:
		r.Error, changed = s.unlink(d, repo, t)
	}
	if fatal != nil {
		return r, changed, fatal
	}
	errKey := key(t.name, t.agent)
	if r.Error != "" {
		r.Action = "failed"
		if st.Errors[errKey] != r.Error {
			st.Errors[errKey] = r.Error
			changed = true
		}
	} else if _, recorded := st.Errors[errKey]; recorded {
		delete(st.Errors, errKey)
		changed = true
	}
	return r, changed, nil
}

// link creates this repository's managed native entry. Each guard returns the
// reason activation stopped; none of them overwrite an unmanaged path. The
// returned error is fatal to the whole pass, not a per-skill failure.
func (s *Store) link(d *database, repo, meta string, st *State, t intent) (string, bool, error) {
	entry, installed := d.Entries[t.name]
	if !installed || entry.ID != t.id {
		return "central content missing; select an installed skill explicitly", false, nil
	}
	changed := false
	if t.record < 0 {
		d.Links = append(d.Links, Activation{Name: t.name, ID: t.id, Repo: repo, Metadata: meta, Context: st.Context, Agent: t.agent, Path: t.path, Target: s.entryPath(entry)})
		t.record = len(d.Links) - 1
		changed = true
	}
	if e := s.verifySiblings(*d, entry); e != nil {
		return e.Error(), changed, nil
	}
	if e := s.verifyRequirements(t.name, entry); e != nil {
		return e.Error(), changed, nil
	}
	if e := checkParents(repo, t.agent); e != nil {
		return e.Error(), changed, nil
	}
	sk, e := ReadSkill(s.entryPath(entry))
	if e != nil {
		return e.Error(), changed, nil
	}
	if sk.Name != t.name {
		return "central skill name changed", changed, nil
	}
	recorded := d.Links[t.record]
	if recorded.ID != t.id {
		return "old activation needs cleanup; run retry", changed, nil
	}
	if Exists(t.path) {
		if !recorded.Created || !samePath(linkTarget(t.path), recorded.Target) {
			return "unmanaged conflict preserved; inspect/adopt the original or move it, then retry", changed, nil
		}
		if e := exclude(repo, t.agent, t.name); e != nil {
			return e.Error(), changed, nil
		}
		return "", changed, nil
	}
	if e := exclude(repo, t.agent, t.name); e != nil {
		return e.Error(), changed, nil
	}
	if e := os.MkdirAll(filepath.Dir(t.path), 0755); e != nil {
		return e.Error(), changed, nil
	}
	// Write ownership ahead of the link, so an interruption between the two
	// leaves a record that the next reconcile can repair.
	d.Links[t.record].Created = true
	if e := s.save(*d); e != nil {
		return "", true, e
	}
	if e := directoryLink(recorded.Target, t.path); e != nil {
		d.Links[t.record].Created = false
		return fmt.Sprintf("native directory link failed; supported local storage required: %v", e), true, nil
	}
	return "", true, nil
}

// unlink removes this repository's managed native entry. A path that is no
// longer the one we created is preserved and reported rather than deleted.
func (s *Store) unlink(d *database, repo string, t intent) (string, bool) {
	recorded := d.Links[t.record]
	if e := checkParents(repo, t.agent); e != nil {
		return e.Error(), false
	}
	if recorded.Created && Exists(t.path) {
		if !samePath(linkTarget(t.path), recorded.Target) {
			return "recorded path was replaced outside Skillverk; preserved", false
		}
		if e := os.Remove(t.path); e != nil {
			return e.Error(), false
		}
	}
	d.Links = slices.Delete(d.Links, t.record, t.record+1)
	return "", true
}

// adoptGlobalAlias transfers a setup-owned global link that already occupies
// this exact native path to repository control, returning its record index and
// whether the index was rewritten and therefore needs persisting.
func adoptGlobalAlias(d *database, repo, meta string, st *State, t intent) (int, bool) {
	for i, a := range d.Links {
		if a.Repo != "" || a.Agent != "global" || !a.Created || a.Pending {
			continue
		}
		if a.ID != t.id || a.Path != t.path || !samePath(linkTarget(t.path), a.Target) {
			continue
		}
		d.Links[i].Repo = repo
		d.Links[i].Metadata = meta
		d.Links[i].Context = st.Context
		d.Links[i].Agent = t.agent
		return i, true
	}
	return -1, false
}

// persist writes the library index and the private worktree state together, so
// an interrupted pass leaves both describing the same set of managed links.
func (s *Store) persist(d *database, meta string, st *State) error {
	if e := s.save(*d); e != nil {
		return e
	}
	return writeJSON(statePath(meta), st)
}

// Reconcile only removes pending deleted activations. It never retries activation.
func (s *Store) Reconcile(repo string) ([]Result, error) {
	u, e := s.lock()
	if e != nil {
		return nil, e
	}
	defer u()
	d, e := s.load()
	if e != nil {
		return nil, e
	}
	globalResults, globalErr := s.reconcileGlobalLinks(&d)
	rs, e := s.collectRemoved(&d)
	rs = append(globalResults, rs...)
	e = errors.Join(e, globalErr)
	if e != nil {
		return rs, e
	}
	if repo == "" {
		return rs, ResultsError(rs)
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
	more, e := s.reconcileLocked(&d, repo, &st, meta)
	rs = append(rs, more...)
	return rs, errors.Join(e, ResultsError(rs))
}
func (s *Store) collectRemoved(d *database) ([]Result, error) {
	var rs []Result
	for id, n := range d.Removing {
		r := Result{Name: n, Path: s.entryPath(Entry{ID: id}), Action: "central content deleted"}
		if current, ok := d.Entries[n]; !ok || current.ID == id {
			if e := s.removeSiblingAlias(n, id); e != nil {
				r.Action = "pending central cleanup"
				r.Error = e.Error()
				rs = append(rs, r)
				continue
			}
		}
		if e := os.RemoveAll(r.Path); e != nil {
			r.Action = "pending central cleanup"
			r.Error = e.Error()
		} else {
			delete(d.Removing, id)
		}
		rs = append(rs, r)
	}
	if len(rs) > 0 {
		if e := s.save(*d); e != nil {
			return rs, e
		}
	}
	return rs, nil
}
func (s *Store) reconcileLocked(d *database, repo string, st *State, meta string) ([]Result, error) {
	var rs []Result
	changed := false
	for i := 0; i < len(d.Links); {
		a := d.Links[i]
		if a.Context != st.Context {
			i++
			continue
		}
		if !a.Pending {
			if a.Repo != repo {
				// Only relocate an index record when the old working tree is gone.
				if _, er := metadata(a.Repo); er != nil {
					a.Repo = repo
					a.Metadata = meta
					a.Path = LinkPath(repo, a.Agent, a.Name)
					d.Links[i] = a
					changed = true
				}
			}
			i++
			continue
		}
		// Context identity travels with private Git metadata when a worktree is moved.
		a.Repo = repo
		a.Metadata = meta
		a.Path = LinkPath(repo, a.Agent, a.Name)
		d.Links[i] = a
		r := s.cleanup(a, st)
		rs = append(rs, r)
		changed = true
		if r.Error == "" {
			d.Links = slices.Delete(d.Links, i, i+1)
		} else {
			i++
		}
	}
	// Covers failed intentions that never produced a link record.
	for n, id := range st.Selected {
		entry, ok := d.Entries[n]
		if !ok || entry.ID != id {
			delete(st.Selected, n)
			for _, a := range Agents {
				delete(st.Errors, key(n, a))
			}
			changed = true
		}
	}
	if changed {
		if e := writeJSON(statePath(meta), st); e != nil {
			return rs, e
		}
		if e := s.save(*d); e != nil {
			return rs, e
		}
	}
	return rs, nil
}
func (s *Store) cleanup(a Activation, st *State) Result {
	r := Result{Name: a.Name, Agent: a.Agent, Path: a.Path, Action: "removed"}
	if st.Context != a.Context || !samePath(st.Library, s.Root) {
		r.Action = "preserved"
		r.Error = "working tree identity changed; recorded path preserved"
		return r
	}
	if st.Selected[a.Name] == a.ID {
		delete(st.Selected, a.Name)
		for _, ag := range Agents {
			delete(st.Errors, key(a.Name, ag))
		}
	}
	if e := checkParents(a.Repo, a.Agent); e != nil {
		r.Error = e.Error()
		return r
	}
	if !a.Created || !Exists(a.Path) {
		return r
	}
	// A newer lifetime or foreign installation must never be removed.
	if !samePath(linkTarget(a.Path), a.Target) {
		r.Action = "preserved"
		r.Error = "original managed link replaced; unrelated path preserved"
		return r
	}
	if e := os.Remove(a.Path); e != nil {
		r.Error = e.Error()
	}
	return r
}
func (s *Store) Delete(name string, confirmed bool) ([]Result, error) {
	return s.DeleteMany([]string{name}, confirmed)
}

// DeleteMany allows a dependent collection, including cycles, to be removed in
// one confirmed operation. Dependencies outside the selection are preserved.
func (s *Store) DeleteMany(names []string, confirmed bool) ([]Result, error) {
	if !confirmed {
		return nil, errors.New("confirm central deletion and removal of recorded activations first")
	}
	if len(names) == 0 {
		return nil, errors.New("select at least one shared skill to delete")
	}
	for _, name := range names {
		if e := ValidateName(name); e != nil {
			return nil, e
		}
	}
	u, e := s.lock()
	if e != nil {
		return nil, e
	}
	defer u()
	d, e := s.load()
	if e != nil {
		return nil, e
	}
	selected := map[string]string{}
	ids := map[string]bool{}
	for _, name := range names {
		entry, ok := d.Entries[name]
		if !ok {
			return nil, fmt.Errorf("%s is not installed", name)
		}
		selected[name] = entry.ID
		ids[entry.ID] = true
	}
	var dependents []string
	for name, entry := range d.Entries {
		if _, ok := selected[name]; ok {
			continue
		}
		for _, ref := range entry.Siblings {
			if _, ok := selected[siblingName(ref)]; ok {
				dependents = append(dependents, name)
				break
			}
		}
	}
	if len(dependents) > 0 {
		slices.Sort(dependents)
		return nil, fmt.Errorf("shared skills depend on this selection: %s; keep their dependencies or delete the collection together with skillverk delete NAME...", strings.Join(dependents, ", "))
	}
	// Mark pending before physical deletion, so interrupted cleanup is recoverable.
	for i := range d.Links {
		if ids[d.Links[i].ID] {
			d.Links[i].Pending = true
		}
	}
	for name, id := range selected {
		d.Removing[id] = name
		delete(d.Entries, name)
	}
	if e = s.save(d); e != nil {
		return nil, e
	}
	var rs []Result
	for i := 0; i < len(d.Links); {
		a := d.Links[i]
		if !ids[a.ID] {
			i++
			continue
		}
		if a.Repo == "" {
			r := removeGlobalLink(a)
			rs = append(rs, r)
			if r.Error == "" {
				d.Links = slices.Delete(d.Links, i, i+1)
			} else {
				i++
			}
			continue
		}
		r := Result{Name: a.Name, Agent: a.Agent, Path: a.Path, Action: "pending cleanup"}
		meta, err := metadata(a.Repo)
		if err == nil && !samePath(meta, a.Metadata) {
			err = errors.New("recorded working tree metadata changed")
		}
		if err == nil {
			var release func()
			release, err = fileLock(filepath.Join(meta, "skillverk.lock"))
			if err == nil {
				st, er := ReadState(a.Repo)
				err = er
				if err == nil {
					r = s.cleanup(a, &st)
					err = writeJSON(statePath(meta), st)
				}
				release()
			}
		}
		if err != nil {
			r.Error = "incomplete cleanup: " + err.Error()
		}
		if r.Error == "" {
			d.Links = slices.Delete(d.Links, i, i+1)
		} else {
			i++
		}
		rs = append(rs, r)
	}
	e = s.save(d)
	more, er := s.collectRemoved(&d)
	rs = append(rs, more...)
	return rs, errors.Join(e, er, ResultsError(rs))
}
