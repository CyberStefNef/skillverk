package library

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// tomlSkillPaths matches the documented top-level array, including multiline
// values.
var tomlSkillPaths = regexp.MustCompile(`(?ms)^skill_paths\s*=\s*(\[.*?\])`)

type ecosystemConfig struct{ Path, Owner, Base string }

func ecosystemConfigs(repo string) []ecosystemConfig {
	h, _ := os.UserHomeDir()
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(h, ".config")
	}
	list := []ecosystemConfig{
		{filepath.Join(h, ".hermes", "config.yaml"), "hermes", h},
		{filepath.Join(configHome, "amp", "settings.json"), "amp", h},
		{filepath.Join(h, ".vibe", "config.toml"), "vibe", h},
		{filepath.Join(h, ".pi", "agent", "settings.json"), "pi", filepath.Join(h, ".pi", "agent")},
		{filepath.Join(configHome, "opencode", "opencode.json"), "opencode", h},
		{filepath.Join(configHome, "opencode", "opencode.jsonc"), "opencode", h},
	}
	if profile := os.Getenv("HERMES_HOME"); profile != "" {
		list = append(list, ecosystemConfig{filepath.Join(profile, "config.yaml"), "hermes", profile})
	}
	profiles, _ := filepath.Glob(filepath.Join(h, ".hermes", "profiles", "*", "config.yaml"))
	for _, p := range profiles {
		list = append(list, ecosystemConfig{p, "hermes", filepath.Dir(p)})
	}
	if repo != "" {
		list = append(list, ecosystemConfig{filepath.Join(repo, ".pi", "settings.json"), "pi", filepath.Join(repo, ".pi")}, ecosystemConfig{filepath.Join(repo, "opencode.json"), "opencode", repo}, ecosystemConfig{filepath.Join(repo, "opencode.jsonc"), "opencode", repo})
	}
	return list
}
func jsonWithoutComments(raw []byte) []byte {
	out := append([]byte(nil), raw...)
	quoted, escaped := false, false
	for i := 0; i < len(out); i++ {
		c := out[i]
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			continue
		}
		if c == '/' && i+1 < len(out) && out[i+1] == '/' {
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
		}
		if c == '/' && i+1 < len(out) && out[i+1] == '*' {
			out[i] = ' '
			i++
			out[i] = ' '
			for i+1 < len(out) {
				i++
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i] = ' '
					i++
					out[i] = ' '
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
			}
		}
	}
	quoted, escaped = false, false
	for i, c := range out {
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(out) && strings.ContainsRune(" \t\r\n", rune(out[j])) {
				j++
			}
			if j < len(out) && (out[j] == ']' || out[j] == '}') {
				out[i] = ' '
			}
		}
	}
	return out
}
func configuredRoots(repo string) ([]ScanRoot, []error) {
	var roots []ScanRoot
	var issues []error
	for _, config := range ecosystemConfigs(repo) {
		raw, err := os.ReadFile(config.Path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			issues = append(issues, err)
			continue
		}
		var doc map[string]any
		if strings.HasSuffix(config.Path, ".toml") {
			doc = map[string]any{}
			if match := tomlSkillPaths.FindSubmatch(raw); len(match) > 1 {
				var values []any
				err = yaml.Unmarshal(match[1], &values)
				doc["skills"] = values
			}

		} else if strings.HasSuffix(config.Path, ".yaml") {
			err = yaml.Unmarshal(raw, &doc)
		} else {
			err = json.Unmarshal(jsonWithoutComments(raw), &doc)
		}
		if err != nil {
			issues = append(issues, fmt.Errorf("cannot read %s skill configuration: %s", config.Owner, config.Path))
			continue
		}
		var paths []any
		if values, ok := doc["amp.skills.path"].([]any); ok {
			paths = append(paths, values...)
		}
		switch value := doc["skills"].(type) {
		case []any:
			paths = append(paths, value...)
		case map[string]any:
			for _, key := range []string{"external_dirs", "paths"} {
				if values, ok := value[key].([]any); ok {
					paths = append(paths, values...)
				}
			}
			if path, ok := value["create_dir"].(string); ok {
				paths = append(paths, path)
			}
		}
		for _, value := range paths {
			path, ok := value.(string)
			if !ok || path == "" {
				continue
			}
			if strings.Contains(path, "://") {
				continue
			} // Inventory does not fetch catalogs.
			missing := false
			path = os.Expand(path, func(key string) string {
				value, ok := os.LookupEnv(key)
				if !ok {
					missing = true
				}
				return value
			})
			if missing {
				issues = append(issues, fmt.Errorf("unset environment variable in %s skill path", config.Path))
				continue
			}
			path = Expand(path)
			if !filepath.IsAbs(path) {
				path = filepath.Join(config.Base, path)
			}
			scope := "global"
			if owner, e := Project(path); e == nil && owner != "" && owner != repo {
				scope = "inherited"
			}
			if repo != "" && within(path, repo) {
				scope = "project"
			}
			roots = append(roots, ScanRoot{filepath.Clean(path), scope, config.Owner})
		}
	}
	return roots, issues
}
func nestedRoots(ctx context.Context, repo string) ([]ScanRoot, error) {
	if repo == "" {
		return nil, nil
	}
	var roots []ScanRoot
	err := filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != repo && (Exists(filepath.Join(path, ".git")) || slices.Contains([]string{".git", "node_modules", "vendor", ".venv", ".cache"}, d.Name())) {
			return filepath.SkipDir
		}
		if d.Name() == "skills" {
			key := filepath.Base(filepath.Dir(path)) + "/skills"
			owner, ok := ecosystemProjects[key]
			if key == ".agents/skills" {
				owner = "codex"
				ok = true
			}
			if key == ".claude/skills" {
				owner = "claude"
				ok = true
			}
			if ok {
				roots = append(roots, ScanRoot{path, "project", owner})
				return filepath.SkipDir
			}
		}
		return nil
	})
	return roots, err
}
