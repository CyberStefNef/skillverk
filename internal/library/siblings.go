package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Named sibling links preserve ../<skill>/... references without exposing more
// skills through agent discovery or changing an entry's stable content address.
func (s *Store) siblingPath(name string) string { return filepath.Join(s.SkillsPath(), name) }
func (s *Store) checkSiblingAlias(name string, entry Entry) error {
	path := s.siblingPath(name)
	if !Exists(path) {
		return nil
	}
	if !samePath(linkTarget(path), s.entryPath(entry)) {
		return fmt.Errorf("shared sibling path is occupied; preserved: %s", path)
	}
	return nil
}
func (s *Store) ensureSiblingAlias(name string, entry Entry) error {
	if e := s.checkSiblingAlias(name, entry); e != nil {
		return e
	}
	if Exists(s.siblingPath(name)) {
		return nil
	}
	return directoryLink(s.entryPath(entry), s.siblingPath(name))
}
func (s *Store) removeSiblingAlias(name, id string) error {
	path := s.siblingPath(name)
	if !Exists(path) {
		return nil
	}
	if !samePath(linkTarget(path), s.entryPath(Entry{ID: id})) {
		return fmt.Errorf("shared sibling path changed; preserved: %s", path)
	}
	return os.Remove(path)
}

var siblingReferencePattern = regexp.MustCompile(`(?:\.\./)+[a-zA-Z0-9_./-]+`)

// Inspect documentation links without executing skill code. Only references to
// actual sibling skills become dependencies; ordinary parent files stay external.
func siblingReferences(source string, known ...string) ([]string, error) {
	return siblingReferencesContext(context.Background(), source, known...)
}
func siblingReferencesContext(ctx context.Context, source string, known ...string) ([]string, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	root, e := filepath.EvalSymlinks(source)
	if e != nil {
		return nil, e
	}
	if info, e := os.Stat(root); e == nil && info.Mode().IsRegular() {
		return nil, nil
	}
	var refs []string
	e = filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		body, e := io.ReadAll(contextReader{ctx, f})
		e = errors.Join(e, f.Close())
		if e != nil {
			return e
		}
		for _, match := range siblingReferencePattern.FindAllString(string(body), -1) {
			target := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(strings.TrimRight(match, "."))))
			rel, e := filepath.Rel(root, target)
			if e != nil {
				return e
			}
			rel = filepath.ToSlash(rel)
			if !strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, "../../") {
				continue
			}
			name := strings.Split(strings.TrimPrefix(rel, "../"), "/")[0]
			sibling, e := ReadSkill(filepath.Join(filepath.Dir(root), name))
			if (e != nil || sibling.Name != name) && !slices.Contains(known, name) && !(ValidateName(name) == nil && strings.HasSuffix(rel, "/SKILL.md") && strings.Contains(string(body), "]("+match)) {
				continue
			}
			if _, e = os.Stat(target); e != nil {
				return fmt.Errorf("sibling reference cannot be read: %s: %w", target, e)
			}
			if !slices.Contains(refs, rel) {
				refs = append(refs, rel)
			}
		}
		return nil
	})
	slices.Sort(refs)
	return refs, e
}
func siblingName(ref string) string { return strings.Split(strings.TrimPrefix(ref, "../"), "/")[0] }
func (s *Store) verifySiblings(d database, entry Entry) error {
	return s.verifySiblingTree(d, entry, map[string]bool{})
}
func (s *Store) verifySiblingTree(d database, entry Entry, seen map[string]bool) error {
	if seen[entry.ID] {
		return nil
	}
	seen[entry.ID] = true
	for _, ref := range entry.Siblings {
		name := siblingName(ref)
		other, ok := d.Entries[name]
		if !ok {
			return fmt.Errorf("missing sibling skill %s; import it from the complete collection", name)
		}
		if e := s.verifySiblingTree(d, other, seen); e != nil {
			return e
		}
		if !samePath(linkTarget(s.siblingPath(name)), s.entryPath(other)) {
			return fmt.Errorf("sibling link for %s is unavailable or changed", name)
		}
		if _, e := os.Stat(s.entryPath(entry) + string(filepath.Separator) + filepath.FromSlash(ref)); e != nil {
			return fmt.Errorf("shared reference %s cannot be read: %w", ref, e)
		}
	}
	return nil
}
func (s *Store) VerifySharedReferences(name string) error {
	u, e := s.lock()
	if e != nil {
		return e
	}
	defer u()
	d, e := s.load()
	if e != nil {
		return e
	}
	entry, ok := d.Entries[name]
	if !ok {
		return errors.New("shared skill not found")
	}
	return s.verifySiblings(d, entry)
}

func (s *Store) prepareSiblingAliases(d database, entry Entry) error {
	for _, ref := range entry.Siblings {
		name := siblingName(ref)
		if other, ok := d.Entries[name]; ok {
			if e := s.ensureSiblingAlias(name, other); e != nil {
				return e
			}
		}
	}
	return nil
}

// Include available dependencies without activating them or replacing existing
// entries implicitly. Validate the complete selection before importing anything.
func (s *Store) dependencySelection(c *Collection, selected []Skill, d database) ([]Skill, error) {
	by := map[string]Skill{}
	for _, sk := range selected {
		by[sk.Name] = sk
	}
	for i := 0; i < len(selected); i++ {
		sk := selected[i]
		var known []string
		for n := range d.Entries {
			known = append(known, n)
		}
		refs, err := siblingReferences(sk.Path, known...)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			name := siblingName(ref)
			if _, ok := by[name]; ok {
				continue
			}
			if entry, ok := d.Entries[name]; ok {
				if err := s.verifySiblings(d, entry); err != nil {
					return nil, err
				}
				suffix := strings.TrimPrefix(ref, "../"+name+"/")
				if _, err := os.Stat(filepath.Join(s.entryPath(entry), filepath.FromSlash(suffix))); err != nil {
					return nil, fmt.Errorf("%s needs %s; update the installed dependency: %w", sk.Name, ref, err)
				}
				continue
			}
			matches, err := c.Select([]string{name})
			if err != nil {
				return nil, fmt.Errorf("%s needs sibling skill %s; import from the complete collection: %w", sk.Name, name, err)
			}
			by[name] = matches[0]
			selected = append(selected, matches[0])
		}
	}
	return selected, nil
}
