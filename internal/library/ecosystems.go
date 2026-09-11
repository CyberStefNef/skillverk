package library

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Ecosystem directories are observations, independent of the agents selected for
// new activations. Migration must preserve each observed native path.
var ecosystemProjects = func() map[string]string {
	paths := map[string]string{".hermes/skills": "hermes", ".codex/skills": "codex"}
	for _, h := range harnessRegistry {
		paths[h.path] = h.id
	}
	return paths
}()

func ecosystemRoots(repo string) []ScanRoot {
	h, _ := os.UserHomeDir()
	var roots []ScanRoot
	globals := map[string]string{
		".config/amp/skills": "amp", ".gemini/config/skills": "antigravity",
		".gemini/antigravity/skills": "antigravity", ".gemini/antigravity-cli/skills": "antigravity",
		".agent/skills": "antigravity", ".hermes/skills": "hermes",
	}
	for _, h := range harnessRegistry {
		if h.global != "" {
			globals[h.global] = h.id
		}
	}
	for path, owner := range globals {
		roots = append(roots, ScanRoot{filepath.Join(h, filepath.FromSlash(path)), "global", owner})
	}
	if configHome := os.Getenv("XDG_CONFIG_HOME"); configHome != "" {
		for _, owner := range []string{"amp", "opencode"} {
			roots = append(roots, ScanRoot{filepath.Join(configHome, owner, "skills"), "global", owner})
		}
	}
	if profile := os.Getenv("HERMES_HOME"); profile != "" {
		roots = append(roots, ScanRoot{filepath.Join(profile, "skills"), "global", "hermes"})
	}
	profiles, _ := filepath.Glob(filepath.Join(h, ".hermes", "profiles", "*", "skills"))
	for _, path := range profiles {
		roots = append(roots, ScanRoot{path, "global", "hermes"})
	}
	if repo != "" {
		for path, owner := range ecosystemProjects {
			roots = append(roots, ScanRoot{filepath.Join(repo, filepath.FromSlash(path)), "project", owner})
		}
	}
	slices.SortFunc(roots, func(a, b ScanRoot) int { return strings.Compare(a.Path, b.Path) })
	return roots
}

// Walk grouping directories, but never follow a directory alias recursively.
func installationPaths(ctx context.Context, root string, owners ...string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if path != root && strings.HasPrefix(d.Name(), ".") && d.Name() != ".system" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if linkTarget(path) != "" {
			paths = append(paths, path)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if Exists(filepath.Join(path, "SKILL.md")) {
				paths = append(paths, path)
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			paths = append(paths, path)
		}
		if d.Type().IsRegular() && strings.HasSuffix(strings.ToLower(d.Name()), ".md") && d.Name() != "SKILL.md" {
			candidate := flatCandidate(path, root)
			if len(owners) > 0 && (owners[0] == "opencode" || owners[0] == "antigravity") && filepath.Dir(path) == root {
				base := strings.ToLower(d.Name())
				candidate = !slices.Contains([]string{"readme.md", "agents.md", "claude.md", "license.md"}, base)
			}
			if _, err := readSkillFile(path); err == nil && candidate {
				paths = append(paths, path)
			}
		}
		return nil
	})
	return paths, err
}

// Preserve an existing client path, including nested scope, without activating
// that skill at the repository root for unrelated clients.
func (s *Store) migrateNative(item SetupItem) ([]Result, error) {
	if item.Repo == "" {
		return s.ShareGlobal(item.Path, item.Action == "replace-shared")
	}
	rel, err := filepath.Rel(item.Repo, item.Path)
	if err != nil || !within(item.Path, item.Repo) {
		return nil, errors.New("native installation is outside the repository")
	}
	restore := func() error { return nil }
	if item.Tracked {
		restore, err = untrackOriginal(item.Repo, item.Path)
		if err != nil {
			return nil, err
		}
	}
	if err = excludeNative(item.Repo, filepath.ToSlash(rel)); err != nil {
		return nil, errors.Join(err, restore())
	}
	local := *s
	local.ScanRoots = []ScanRoot{{filepath.Dir(item.Path), "global", item.Owner}}
	results, err := local.ShareGlobal(item.Path, item.Action == "replace-shared")
	if err != nil {
		central, _ := s.Path(item.Name)
		target := linkTarget(item.Path)
		if central == "" || (!samePath(target, central) && !samePath(target, filepath.Join(central, "SKILL.md"))) {
			err = errors.Join(err, restore())
		}
	}
	for i := range results {
		if results[i].Action == "global shared link active" {
			results[i].Action = "native shared link active"
		}
		if strings.Contains(results[i].Action, "global original retained") {
			results[i].Action = "original retained until native link verification"
		}
	}
	return results, err
}

func ecosystemDirectory(name string) bool {
	for path := range ecosystemProjects {
		if strings.Split(path, "/")[0] == name {
			return true
		}
	}
	return name == ".amp"
}

func installedBundleOwner(path string) string {
	for parent := path; ; parent = filepath.Dir(parent) {
		normalized := filepath.ToSlash(parent)
		if strings.Contains(normalized, "/.tessl/") && (Exists(filepath.Join(parent, "tile.json")) || Exists(filepath.Join(parent, ".tessl-plugin", "plugin.json"))) {
			return "Tessl"
		}
		if (strings.Contains(normalized, "/.pi/agent/") || strings.Contains(normalized, "/.pi/")) && Exists(filepath.Join(parent, "package.json")) {
			return "Pi"
		}
		if Exists(filepath.Join(parent, "plugin.json")) && (strings.Contains(normalized, "/plugins/") || strings.Contains(normalized, "/extensions/")) {
			return "Agent Plugin"
		}
		for dir, owner := range map[string]string{".cursor-plugin": "Cursor", ".claude-plugin": "Claude", ".codex-plugin": "Codex", ".tessl-plugin": "Tessl"} {
			if Exists(filepath.Join(parent, dir, "plugin.json")) && (strings.Contains(normalized, "/plugins/") || strings.Contains(normalized, "/extensions/")) {
				return owner
			}
		}
		if Exists(filepath.Join(parent, "gemini-extension.json")) && strings.Contains(normalized, "/.gemini/extensions/") {
			return "Gemini"
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	return ""
}
