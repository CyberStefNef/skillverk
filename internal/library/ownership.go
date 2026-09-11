package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Localize creates a Git-tracked repository snapshot. Shared content and other
// repositories are unaffected; this repository no longer follows shared updates.
func (s *Store) Localize(repo, name string, confirmed bool) ([]Result, error) {
	if !confirmed {
		return nil, errors.New("confirm creating and staging a repository-owned copy first")
	}
	if e := ValidateName(name); e != nil {
		return nil, e
	}
	unlock, e := s.lock()
	if e != nil {
		return nil, e
	}
	defer unlock()
	meta, e := metadata(repo)
	if e != nil {
		return nil, e
	}
	release, e := fileLock(filepath.Join(meta, "skillverk.lock"))
	if e != nil {
		return nil, e
	}
	defer release()
	d, e := s.load()
	if e != nil {
		return nil, e
	}
	st, _, e := s.state(repo)
	if e != nil {
		return nil, e
	}
	entry, ok := d.Entries[name]
	if !ok {
		return nil, fmt.Errorf("unknown shared skill %s", name)
	}
	if len(entry.Siblings) > 0 {
		return nil, errors.New("this skill depends on shared sibling skills; keep it shared until the collection can be localized together")
	}
	sk, e := ReadSkill(s.entryPath(entry))
	if e != nil {
		return nil, e
	}
	if sk.Name != name {
		return nil, errors.New("shared skill name changed")
	}
	agents := slices.Clone(st.Agents)
	// Include existing owned exposure even if a previous agent change was partial.
	for _, a := range d.Links {
		if a.Repo == repo && a.Context == st.Context && a.Name == name && !slices.Contains(agents, a.Agent) {
			agents = append(agents, a.Agent)
		}
	}
	if len(agents) == 0 {
		return nil, errors.New("choose repository agents first")
	}
	var paths []string
	for _, agent := range agents {
		if e = checkParents(repo, agent); e != nil {
			return nil, e
		}
		p := LinkPath(repo, agent, name)
		tracked, err := gitAt(repo, "ls-files", "--", agentPaths[agent]+"/"+name)
		if err != nil {
			return nil, err
		}
		if tracked != "" {
			return nil, fmt.Errorf("repository-owned path already exists: %s", p)
		}
		if Exists(p) {
			idx := findRecord(&d, st, repo, name, agent)
			if idx < 0 || !d.Links[idx].Created || !samePath(linkTarget(p), d.Links[idx].Target) {
				return nil, fmt.Errorf("existing repository path preserved: %s", p)
			}
		}
		paths = append(paths, p)
	}
	tmp, e := os.MkdirTemp(meta, "skillverk-localize-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(tmp)
	prepared := filepath.Join(tmp, "content")
	if e = copyTree(s.entryPath(entry), prepared); e != nil {
		return nil, e
	}
	// Prepare relative native links before changing any exposure.
	for i := 1; i < len(paths); i++ {
		rel, err := filepath.Rel(filepath.Dir(paths[i]), paths[0])
		if err != nil {
			return nil, err
		}
		if e = relativeDirectoryLink(rel, filepath.Join(tmp, fmt.Sprint(i))); e != nil {
			return nil, fmt.Errorf("repository link unavailable: %w", e)
		}
	}
	replaced := 0
	rollback := func() {
		for i := replaced - 1; i >= 0; i-- {
			_ = os.RemoveAll(paths[i])
			backup := filepath.Join(tmp, fmt.Sprintf("old-%d", i))
			if Exists(backup) {
				_ = os.Rename(backup, paths[i])
			}
		}
	}
	for i, p := range paths {
		if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			rollback()
			return nil, e
		}
		if Exists(p) {
			if e = os.Rename(p, filepath.Join(tmp, fmt.Sprintf("old-%d", i))); e != nil {
				rollback()
				return nil, e
			}
		}
		replaced = i + 1
		src := prepared
		if i > 0 {
			src = filepath.Join(tmp, fmt.Sprint(i))
		}
		if e = os.Rename(src, p); e != nil {
			rollback()
			return nil, e
		}
	}
	args := []string{"add", "-f", "--"}
	for _, p := range paths {
		rel, _ := filepath.Rel(repo, p)
		args = append(args, filepath.ToSlash(rel))
	}
	if _, e = gitAt(repo, args...); e != nil {
		rollback()
		return nil, e
	}
	delete(st.Selected, name)
	for _, a := range Agents {
		delete(st.Errors, key(name, a))
	}
	d.Links = slices.DeleteFunc(d.Links, func(a Activation) bool { return a.Repo == repo && a.Context == st.Context && a.Name == name })
	if e = writeJSON(statePath(meta), st); e != nil {
		return nil, e
	}
	if e = s.save(d); e != nil {
		return nil, e
	}
	var rs []Result
	for i, p := range paths {
		rs = append(rs, Result{Name: name, Agent: agents[i], Path: p, Action: "repository-owned copy staged in Git"})
	}
	return rs, nil
}

// Ownership describes storage in this repository, independently of activation.
func (s Skill) Ownership() string {
	if s.HasScope("project") {
		return "repository"
	}
	if s.Entry != nil {
		return "shared"
	}
	return "external"
}
