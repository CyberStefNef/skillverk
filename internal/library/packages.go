package library

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var npmPackageName = regexp.MustCompile(`^(?:@[a-zA-Z0-9_.-]+/)?[a-zA-Z0-9_.-]+$`)

// Fetch npm's published archive without invoking npm or package lifecycle hooks.
func npmSource(ctx context.Context, value string) (string, func(), error) {
	spec := strings.TrimPrefix(value, "npm:")
	name, version := spec, "latest"
	if i := strings.LastIndex(spec, "@"); i > 0 {
		name, version = spec[:i], spec[i+1:]
	}
	if !npmPackageName.MatchString(name) || version == "" {
		return "", nil, errors.New("invalid npm package source")
	}
	endpoint := "https://registry.npmjs.org/" + url.PathEscape(name) + "/" + url.PathEscape(version)
	raw, err := fetchSkillBytes(ctx, endpoint)
	if err != nil {
		return "", nil, err
	}
	var metadata struct {
		Dist struct {
			Tarball   string `json:"tarball"`
			Integrity string `json:"integrity"`
		} `json:"dist"`
	}
	if err = json.Unmarshal(raw, &metadata); err != nil {
		return "", nil, err
	}
	raw, err = fetchReferencedSkillBytes(ctx, endpoint, metadata.Dist.Tarball)
	if err != nil {
		return "", nil, err
	}
	sum := sha512.Sum512(raw)
	if metadata.Dist.Integrity != "sha512-"+base64.StdEncoding.EncodeToString(sum[:]) {
		return "", nil, errors.New("npm package integrity mismatch or unsupported integrity algorithm")
	}
	tmp, err := TempDir("npm-source-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	if err = unpackSkills(ctx, raw, tmp); err != nil {
		cleanup()
		return "", nil, err
	}
	return unwrapArchive(tmp), cleanup, nil
}
func packageSkillPatterns(root string) ([]string, bool, error) {
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		return nil, false, nil
	}
	// Explicit manifest paths replace convention discovery.
	for _, file := range []string{"package.json", ".cursor-plugin/plugin.json", ".tessl-plugin/plugin.json", "plugin.json"} {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		var doc map[string]json.RawMessage
		if err = json.Unmarshal(raw, &doc); err != nil {
			return nil, false, fmt.Errorf("invalid package manifest %s: %w", file, err)
		}
		if file == "package.json" {
			raw, ok := doc["pi"]
			if !ok {
				continue
			}
			if err = json.Unmarshal(raw, &doc); err != nil {
				return nil, false, err
			}
		}
		raw, ok := doc["skills"]
		if !ok {
			continue
		}
		var patterns []string
		if json.Unmarshal(raw, &patterns) != nil {
			var path string
			if err = json.Unmarshal(raw, &path); err != nil {
				return nil, false, err
			}
			patterns = []string{path}
		}
		for _, p := range patterns {
			p = strings.TrimPrefix(strings.TrimPrefix(p, "!"), "./")
			if filepath.IsAbs(p) || strings.Contains(p, "..") || strings.Contains(p, "\\") {
				return nil, false, errors.New("unsafe package skill pattern")
			}
		}
		return patterns, true, nil
	}
	return nil, false, nil
}
func packageSkillFilter(root string, skills []Skill) ([]Skill, error) {
	patterns, explicit, err := packageSkillPatterns(root)
	if err != nil {
		return nil, err
	}
	if !explicit {
		return skills, nil
	}
	var selected []Skill
	for _, sk := range skills {
		rel, _ := filepath.Rel(root, sk.Path)
		rel = filepath.ToSlash(rel)
		included := false
		for _, p := range patterns {
			exclude := strings.HasPrefix(p, "!")
			p = strings.TrimPrefix(strings.TrimPrefix(p, "!"), "./")
			match := packagePathMatch(p, rel)
			if !match && filepath.Ext(sk.Path) != ".md" {
				match = packagePathMatch(p, rel+"/SKILL.md")
			}
			if match {
				included = !exclude
			}
		}
		if included {
			selected = append(selected, sk)
		}
	}
	return selected, nil
}

func explicitPackageSkills(ctx context.Context, root string, skills []Skill) ([]Skill, error) {
	patterns, _, err := packageSkillPatterns(root)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, skill := range skills {
		seen[skill.Path] = true
	}
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") {
			continue
		}
		prefix := strings.TrimPrefix(pattern, "./")
		if i := strings.IndexAny(prefix, "*?["); i >= 0 {
			prefix = prefix[:i]
			prefix = prefix[:strings.LastIndex(prefix, "/")+1]
		}
		if prefix == "" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(prefix))
		if !Exists(path) {
			continue
		}
		resolved, e := resolvePath(path)
		if e != nil {
			return nil, e
		}
		if !within(resolved, root) {
			return nil, errors.New("package skill path escapes its source")
		}
		found, _, e := discoverContext(ctx, path)
		if e != nil {
			return nil, e
		}
		for _, skill := range found {
			if !seen[skill.Path] {
				skills = append(skills, skill)
				seen[skill.Path] = true
			}
		}
	}
	return skills, nil
}

func packagePathMatch(pattern, value string) bool {
	var match func([]string, []string) bool
	match = func(p, v []string) bool {
		if len(p) == 0 {
			return len(v) == 0
		}
		if p[0] == "**" {
			return match(p[1:], v) || (len(v) > 0 && match(p, v[1:]))
		}
		if len(v) == 0 {
			return false
		}
		ok, err := path.Match(p[0], v[0])
		return err == nil && ok && match(p[1:], v[1:])
	}
	p := strings.Split(strings.TrimRight(pattern, "/"), "/")
	for {
		if match(p, strings.Split(value, "/")) {
			return true
		}
		i := strings.LastIndex(value, "/")
		if i < 0 {
			return false
		}
		value = value[:i]
	}
}
