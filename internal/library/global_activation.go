package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Check existing ancestors before creating or removing a user-account link.
func checkGlobalParents(path string) error {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0 || linkTarget(parent) != "") {
			return fmt.Errorf("preserved non-directory or linked global parent %s", parent)
		}
		if filepath.Dir(parent) == parent {
			return nil
		}
	}
}

// Setup also records nested repository aliases as Agent=global. Only actual
// user-account installations belong to global selection.
func (s *Store) globalHarness(link Activation) (string, bool) {
	if link.Repo != "" {
		return "", false
	}
	if link.Harness != "" {
		return link.Harness, true
	}
	for _, agent := range Agents {
		root, err := GlobalRoot(agent)
		if err == nil && within(link.Path, root) {
			return agent, true
		}
	}
	for _, root := range s.roots("") {
		if root.Scope == "global" && root.Owner != "admin" && within(link.Path, root.Path) {
			return root.Owner, true
		}
	}
	return "", false
}

func (s *Store) verifyGlobalSkill(d database, name string, entry Entry) error {
	sk, err := ReadSkill(s.entryPath(entry))
	if err != nil {
		return err
	}
	if sk.Name != name {
		return errors.New("central skill name changed")
	}
	return errors.Join(s.verifySiblings(d, entry), s.verifyRequirements(name, entry))
}

// SelectGlobal manages user-account links independently of repository state.
// An empty harness selection enables the defaults, or disables every recorded
// global link for the named skills. Existing unmanaged paths are preserved.
func (s *Store) SelectGlobal(names, harnesses []string, on bool) ([]Result, error) {
	for _, name := range names {
		if err := ValidateName(name); err != nil {
			return nil, err
		}
	}
	for _, agent := range harnesses {
		if !slices.Contains(Agents, agent) {
			return nil, fmt.Errorf("unknown harness %s", agent)
		}
	}
	if on && len(harnesses) == 0 {
		harnesses = DefaultAgents
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	d, err := s.load()
	if err != nil {
		return nil, err
	}
	if !on {
		for i, link := range d.Links {
			agent, global := s.globalHarness(link)
			if global && slices.Contains(names, link.Name) && (len(harnesses) == 0 || slices.Contains(harnesses, agent)) {
				d.Links[i].Pending = true
			}
		}
		if err := s.save(d); err != nil {
			return nil, err
		}
		return s.reconcileGlobalLinks(&d)
	}
	// Validate the full request before recording any activation intent.
	for _, name := range names {
		entry, ok := d.Entries[name]
		if !ok {
			return nil, fmt.Errorf("%s is not installed", name)
		}
		if err := s.verifyGlobalSkill(d, name, entry); err != nil {
			return nil, err
		}
	}
	var indices []int
	for _, name := range names {
		entry := d.Entries[name]
		for _, agent := range Agents {
			if !slices.Contains(harnesses, agent) {
				continue
			}
			root, err := GlobalRoot(agent)
			if err != nil {
				return nil, err
			}
			path := filepath.Join(root, name)
			index := -1
			for i, link := range d.Links {
				if link.Repo == "" && samePath(link.Path, path) {
					if link.ID != entry.ID || link.Name != name || link.Target != s.entryPath(entry) {
						return nil, fmt.Errorf("old global activation needs cleanup: %s", path)
					}
					index = i
					break
				}
			}
			if index < 0 {
				d.Links = append(d.Links, Activation{Name: name, ID: entry.ID, Agent: "global", Path: path, Target: s.entryPath(entry)})
				index = len(d.Links) - 1
			}
			d.Links[index].Harness = agent
			d.Links[index].Pending = false
			if !slices.Contains(indices, index) {
				indices = append(indices, index)
			}
		}
	}
	if err := s.save(d); err != nil {
		return nil, err
	}
	return s.applyGlobal(&d, indices)
}

func (s *Store) RetryGlobal() ([]Result, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	d, err := s.load()
	if err != nil {
		return nil, err
	}
	results, err := s.reconcileGlobalLinks(&d)
	if err != nil {
		return results, err
	}
	var indices []int
	for i, link := range d.Links {
		if link.Repo == "" && link.Harness != "" && !link.Pending {
			indices = append(indices, i)
		}
	}
	more, err := s.applyGlobal(&d, indices)
	return append(results, more...), err
}

func (s *Store) applyGlobal(d *database, indices []int) ([]Result, error) {
	var results []Result
	for _, index := range indices {
		link := &d.Links[index]
		result := Result{Name: link.Name, Agent: link.Harness, Path: link.Path, Action: "global link active"}
		entry, ok := d.Entries[link.Name]
		var failure error
		if !ok || entry.ID != link.ID {
			failure = errors.New("central content missing; import the skill again")
		} else {
			failure = errors.Join(s.verifyGlobalSkill(*d, link.Name, entry), checkGlobalParents(link.Path))
		}
		if failure == nil && Exists(link.Path) && (!link.Created || !samePath(linkTarget(link.Path), link.Target)) {
			failure = errors.New("unmanaged global conflict preserved; inspect/adopt the original or move it, then retry --global")
		}
		if failure == nil && !Exists(link.Path) {
			failure = os.MkdirAll(filepath.Dir(link.Path), 0755)
			if failure == nil {
				// Persist ownership before creating the link, just as for repositories.
				link.Created = true
				if err := s.save(*d); err != nil {
					return results, err
				}
				if failure = directoryLink(link.Target, link.Path); failure != nil {
					link.Created = false
				}
			}
		}
		link.Error = ""
		if failure != nil {
			link.Error = failure.Error()
			result.Action = "failed"
			result.Error = link.Error
		}
		results = append(results, result)
		if err := s.save(*d); err != nil {
			return results, err
		}
	}
	return results, ResultsError(results)
}
