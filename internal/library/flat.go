package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Convert the entry point of a flat skill while copying referenced local
// resources. Never copy the surrounding working tree as an implicit bundle.
var flatResource = regexp.MustCompile("(?:\\.\\./|\\./)?[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+|[A-Za-z0-9_-]+\\.[A-Za-z0-9]{1,10}")

func copySkill(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyTree(src, dst)
	}
	if err = os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	raw, err = normalizeFlatEntry(raw, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)))
	if err != nil {
		return err
	}
	if _, err = readSkillFile(src); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dst, "SKILL.md"), raw, info.Mode().Perm()); err != nil {
		return err
	}
	root, err := resolvePath(filepath.Dir(src))
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var copyReferences func([]byte, string) error
	copyReferences = func(body []byte, base string) error {
		for _, ref := range flatResource.FindAllString(string(body), -1) {
			ref = strings.TrimRight(ref, ".")
			from := filepath.Clean(filepath.Join(base, filepath.FromSlash(ref)))
			if !within(from, root) {
				return fmt.Errorf("flat skill references an outside resource %s; preserve its original layout", ref)
			}
			if !Exists(from) {
				continue
			} // URL examples and tool paths are not local resources.
			resolved, e := resolvePath(from)
			if e != nil {
				return e
			}
			if !within(resolved, root) {
				return errors.New("flat skill resource escapes its source")
			}
			rel, _ := filepath.Rel(root, from)
			if seen[rel] {
				continue
			}
			seen[rel] = true
			to := filepath.Join(dst, rel)
			if Exists(to) {
				continue
			}
			if e = os.MkdirAll(filepath.Dir(to), 0755); e != nil {
				return e
			}
			stat, e := os.Stat(resolved)
			if e != nil {
				return e
			}
			if stat.IsDir() {
				if e = copyTree(resolved, to); e != nil {
					return e
				}
				continue
			}
			if !stat.Mode().IsRegular() {
				return errors.New("flat skill has a special resource")
			}
			b, e := os.ReadFile(resolved)
			if e != nil {
				return e
			}
			if e = os.WriteFile(to, b, stat.Mode().Perm()); e != nil {
				return e
			}
			if strings.HasSuffix(rel, ".md") {
				if e = copyReferences(b, filepath.Dir(from)); e != nil {
					return e
				}
			}
		}
		return nil
	}
	return copyReferences(raw, root)
}

func portableDigest(ctx context.Context, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return digestContext(ctx, path)
	}
	tmp, err := TempDir("flat-preview-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	dst := filepath.Join(tmp, "content")
	if err = copySkill(path, dst); err != nil {
		return "", err
	}
	return digestContext(ctx, dst)
}

func flatCandidate(path, root string) bool {
	if path == root {
		return true
	}
	parent := filepath.Dir(path)
	eligible := parent == root
	for dir := parent; !eligible && within(dir, root); dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "skills" {
			eligible = true
		}
		if dir == root || filepath.Dir(dir) == dir {
			break
		}
	}
	if !eligible {
		return false
	}
	base := strings.ToLower(filepath.Base(path))
	if base == "readme.md" || base == "agents.md" || base == "claude.md" || base == "license.md" {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	// A direct flat source may omit metadata. Discovery among other files needs
	// declared skill metadata so ordinary documentation is not turned into skills.
	return strings.HasPrefix(strings.TrimSpace(string(raw)), "---") && strings.Contains(string(raw), "description:")
}
