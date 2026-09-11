package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// ShareGlobal preserves native global availability while replacing a standalone
// installation with a recorded link to the shared library.
func (s *Store) ShareGlobal(path string, replace bool) ([]Result, error) {
	var err error
	path, err = filepath.Abs(Expand(path))
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if !samePath(resolved, filepath.Dir(path)) {
		return nil, errors.New("linked global ancestor preserved")
	}
	info, e := os.Lstat(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("linked global parent preserved")
	}
	sourceInfo, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	flat := sourceInfo.Mode().IsRegular()
	rs, e := s.Adopt("", path, replace)
	if e != nil {
		return rs, e
	}
	id, e := func() (string, error) {
		unlock, e := s.lock()
		if e != nil {
			return "", e
		}
		defer unlock()
		d, e := s.load()
		if e != nil {
			return "", e
		}
		idx := -1
		for i, o := range d.Originals {
			if o.Scope == "global" && samePath(o.Path, path) {
				idx = i
			}
		}
		if idx < 0 {
			return "", errors.New("global adoption record missing")
		}
		o := d.Originals[idx]
		entry := d.Entries[o.Name]
		target := s.entryPath(entry)
		if flat {
			target = filepath.Join(target, "SKILL.md")
		}
		actual, e := digest(path)
		if e != nil {
			return "", e
		}
		if actual != o.Digest {
			return "", errors.New("original changed; preserved")
		}
		tmp, e := os.MkdirTemp(filepath.Dir(path), ".skillverk-original-")
		if e != nil {
			return "", e
		}
		o.Backup = filepath.Join(tmp, "original")
		d.Originals[idx] = o
		if e = s.save(d); e != nil {
			_ = os.Remove(tmp)
			return "", e
		}
		if e = os.Rename(path, o.Backup); e != nil {
			d.Originals[idx].Backup = ""
			return "", errors.Join(e, s.save(d))
		}
		rollback := func(cause error) error {
			if samePath(linkTarget(path), target) {
				if er := os.Remove(path); er != nil {
					return errors.Join(cause, er)
				}
			}
			if Exists(path) {
				return cause
			}
			if er := os.Rename(o.Backup, path); er != nil {
				return errors.Join(cause, er)
			}
			d.Links = slices.DeleteFunc(d.Links, func(a Activation) bool { return a.Repo == "" && samePath(a.Path, path) })
			d.Originals[idx].Backup = ""
			entry.Source = path
			d.Entries[o.Name] = entry
			_ = os.Remove(tmp)
			return errors.Join(cause, s.save(d))
		}
		if flat {
			e = os.Symlink(target, path)
		} else {
			e = directoryLink(target, path)
		}
		if e != nil {
			return "", rollback(e)
		}
		d.Links = append(d.Links, Activation{Name: o.Name, ID: entry.ID, Agent: "global", Path: path, Target: target, Created: true})
		entry.Source = o.Backup
		entry.Subpath = "."
		d.Entries[o.Name] = entry
		if e = s.save(d); e != nil {
			return "", rollback(e)
		}
		return o.ID, nil
	}()
	if e != nil {
		return rs, e
	}
	r, e := s.CleanupOriginal(id, true)
	rs = append(rs, r)
	if e == nil {
		rs = append(rs, Result{Path: path, Action: "global shared link active"})
	}
	return rs, e
}

func removeGlobalLink(a Activation) Result {
	r := Result{Name: a.Name, Agent: "global", Path: a.Path, Action: "global link removed"}
	if !Exists(a.Path) {
		return r
	}
	if !a.Created || !samePath(linkTarget(a.Path), a.Target) {
		r.Error = "global path changed; preserved"
		return r
	}
	if e := os.Remove(a.Path); e != nil {
		r.Error = e.Error()
	}
	return r
}
func (s *Store) reconcileGlobalLinks(d *database) ([]Result, error) {
	var rs []Result
	for i := 0; i < len(d.Links); {
		a := d.Links[i]
		if a.Repo != "" || !a.Pending {
			i++
			continue
		}
		r := removeGlobalLink(a)
		rs = append(rs, r)
		if r.Error == "" {
			d.Links = slices.Delete(d.Links, i, i+1)
		} else {
			i++
		}
	}
	if len(rs) > 0 {
		if e := s.save(*d); e != nil {
			return rs, fmt.Errorf("save global cleanup: %w", e)
		}
	}
	return rs, ResultsError(rs)
}
