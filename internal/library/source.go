package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	shorthandRepository = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)
	commitSHA           = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
)

// TempDir keeps transient source files in the app cache, away from small
// system tmpfs quotas. Callers remove the returned directory when finished.
func TempDir(prefix string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(cache, "skillverk", "tmp")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, prefix)
}

// SourceContext fetches only when given an explicit HTTPS URL or owner/repo
// shorthand. It never runs a package manager, a source script, or submodule
// initialization.
func SourceContext(parent context.Context, value string) (string, func(), error) {
	if err := parent.Err(); err != nil {
		return "", nil, err
	}
	if strings.HasPrefix(value, "npm:") {
		return npmSource(parent, value)
	}
	if strings.HasPrefix(value, "git:") {
		normalized, err := piGitSource(value)
		if err != nil {
			return "", nil, err
		}
		value = normalized
	}
	local := Expand(value)
	if !strings.Contains(value, "://") && Exists(local) {
		if archiveURL(local) {
			return archiveSource(parent, local, true)
		}
		return local, func() {}, nil
	}
	if shorthandRepository.MatchString(value) {
		value = "https://github.com/" + value + ".git"
	}
	if isServiceURL(value) {
		return serviceSource(parent, value)
	}
	remote, ref, subpath, parseErr := repositoryURL(value)
	if parseErr != nil {
		return "", nil, parseErr
	}
	if !strings.HasPrefix(remote, "https://") {
		return "", nil, errors.New("source must be a local path, HTTPS Git URL, or owner/repo")
	}
	tmp, err := TempDir("source-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	args := []string{"-c", "core.hooksPath=" + os.DevNull, "clone", "--depth=1"}
	if subpath != "" {
		args = append(args, "--filter=blob:none", "--no-checkout")
	}
	commitRef := commitSHA.MatchString(ref)
	if ref != "" && !commitRef {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", remote, tmp)
	if err = sourceGit(ctx, args...); err != nil {
		cleanup()
		return "", nil, err
	}
	if commitRef {
		if err = sourceGit(ctx, "-C", tmp, "fetch", "--depth=1", "origin", ref); err != nil {
			cleanup()
			return "", nil, err
		}
		if err = sourceGit(ctx, "-C", tmp, "-c", "core.hooksPath="+os.DevNull, "checkout", "--detach", ref); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	if subpath != "" {
		sparsePath := subpath
		probe := exec.CommandContext(ctx, "git", "-C", tmp, "cat-file", "-e", "HEAD:"+subpath+"/SKILL.md")
		probe.Env = gitEnv()
		if probe.Run() == nil {
			sparsePath = filepath.ToSlash(filepath.Dir(subpath))
		}
		if sparsePath == "." {
			err = sourceGit(ctx, "-C", tmp, "sparse-checkout", "disable")
		} else {
			err = sourceGit(ctx, "-C", tmp, "sparse-checkout", "set", "--cone", "--", sparsePath)
		}
		if err == nil {
			err = sourceGit(ctx, "-C", tmp, "-c", "core.hooksPath="+os.DevNull, "checkout", "--force", "HEAD")
		}
		if err != nil {
			cleanup()
			return "", nil, err
		}
	}
	root := filepath.Join(tmp, filepath.FromSlash(subpath))
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if !within(resolved, tmp) {
		cleanup()
		return "", nil, errors.New("source path escapes repository")
	}
	return resolved, cleanup, nil
}

func discoverContext(ctx context.Context, source string) ([]Skill, []Result, error) {
	var skipped []Result
	source, err := filepath.Abs(Expand(source))
	if err != nil {
		return nil, nil, err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return nil, nil, err
	}
	if filepath.Base(source) == "SKILL.md" {
		source = filepath.Dir(source)
	}
	skills := []Skill{}
	err = filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if err != nil {
			return err
		}
		if !d.IsDir() {
			if d.Type().IsRegular() && strings.HasSuffix(strings.ToLower(d.Name()), ".md") && d.Name() != "SKILL.md" {
				if sk, e := readSkillFile(path); e == nil && flatCandidate(path, source) {
					skills = append(skills, sk)
				} else if path == source {
					return e
				}
			}
			return nil
		}
		if path != source && ((strings.HasPrefix(d.Name(), ".") && d.Name() != ".curated" && d.Name() != ".experimental" && d.Name() != ".agents" && d.Name() != ".claude" && d.Name() != ".github" && d.Name() != ".codex" && !ecosystemDirectory(d.Name())) || slices.Contains([]string{"node_modules", "target", "vendor", "venv"}, d.Name())) {
			return filepath.SkipDir
		}
		if Exists(filepath.Join(path, "SKILL.md")) {
			skill, err := ReadSkill(path)
			if err != nil {
				if path == source {
					return fmt.Errorf("%s: %w", path, err)
				}
				rel, _ := filepath.Rel(source, path)
				skipped = append(skipped, Result{Path: filepath.ToSlash(rel), Action: "skipped invalid skill", Error: err.Error()})
				return filepath.SkipDir
			}
			skills = append(skills, skill)
			return filepath.SkipDir
		}
		return nil
	})
	if err == nil && len(skills) == 0 && len(skipped) > 0 {
		var problems []error
		for _, p := range skipped {
			problems = append(problems, fmt.Errorf("%s: %s", p.Path, p.Error))
		}
		err = fmt.Errorf("no valid skills found: %w", errors.Join(problems...))
	}
	if err == nil && len(skills) == 0 {
		err = errors.New("source contains no supported skills")
	}
	return skills, skipped, err
}

// Collection owns a fetched checkout until Close. Both CLI and TUI select from
// this same snapshot, avoiding an unreviewed refetch between browse and install.
type Collection struct {
	DefaultNames []string
	Source       string
	Root         string
	Commit       string
	Skills       []Skill
	Skipped      []Result
	cleanup      func()
}

func (c *Collection) Close() {
	if c != nil && c.cleanup != nil {
		c.cleanup()
	}
}
func OpenCollection(value string) (*Collection, error) {
	return OpenCollectionContext(context.Background(), value)
}

func OpenCollectionContext(ctx context.Context, value string) (*Collection, error) {
	root, cleanup, err := SourceContext(ctx, value)
	if err != nil {
		return nil, err
	}
	c := &Collection{Source: value, Root: root, cleanup: cleanup}
	if Exists(Expand(value)) {
		c.Source, err = filepath.Abs(Expand(value))
		if err != nil {
			cleanup()
			return nil, err
		}
	} else {
		if shorthandRepository.MatchString(value) {
			c.Source = "https://github.com/" + value + ".git"
		}
		if !isServiceURL(value) && !strings.HasPrefix(value, "npm:") {
			out, e := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD").Output()
			if e != nil {
				cleanup()
				return nil, e
			}
			c.Commit = strings.TrimSpace(string(out))
		}
	}
	if filepath.Base(root) == "SKILL.md" {
		root = filepath.Dir(root)
		c.Root = root
	}
	c.Root, err = filepath.Abs(c.Root)
	if err != nil {
		cleanup()
		return nil, err
	}
	c.Root, err = filepath.EvalSymlinks(c.Root)
	if err != nil {
		cleanup()
		return nil, err
	}
	if Exists(Expand(value)) && !archiveURL(value) {
		c.Source = c.Root
	}
	if isServiceURL(value) {
		_ = readJSON(filepath.Join(c.Root, ".skillverk-selection.json"), &c.DefaultNames)
	}
	c.Skills, c.Skipped, err = discoverContext(ctx, c.Root)
	if err == nil || (ctx.Err() == nil && err.Error() == "source contains no supported skills") {
		expanded, expansionErr := explicitPackageSkills(ctx, c.Root, c.Skills)
		if expansionErr != nil {
			err = expansionErr
		} else if len(expanded) > 0 {
			c.Skills = expanded
			err = nil
		}
	}
	if err == nil {
		c.Skills, err = packageSkillFilter(c.Root, c.Skills)
		if err == nil && len(c.Skills) == 0 {
			err = errors.New("package manifest selects no supported skills")
		}
	}
	if err != nil {
		cleanup()
		return nil, err
	}
	// A direct skill source still needs its sibling resources. Keep the default
	// selection narrow while exposing the sibling collection to dependency import.
	if len(c.Skills) == 1 && samePath(c.Skills[0].Path, c.Root) {
		refs, e := siblingReferencesContext(ctx, c.Root)
		if e != nil {
			cleanup()
			return nil, e
		}
		if len(refs) > 0 {
			name := c.Skills[0].Name
			parent := filepath.Dir(c.Root)
			siblings, skipped, e := discoverContext(ctx, parent)
			if e != nil {
				cleanup()
				return nil, e
			}
			c.Root = parent
			c.Skills = siblings
			c.Skipped = append(c.Skipped, skipped...)
			c.DefaultNames = []string{name}
		}
	}
	slices.SortFunc(c.Skills, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })
	return c, nil
}
func (c *Collection) Select(names []string) ([]Skill, error) {
	if len(names) == 0 && len(c.DefaultNames) > 0 {
		names = c.DefaultNames
	}
	if len(names) == 0 {
		if len(c.Skills) != 1 {
			return nil, fmt.Errorf("source has %d skills; browse with add SOURCE --list, select --skill NAME, or use --all", len(c.Skills))
		}
		return c.Skills, nil
	}
	chosen := []Skill{}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		matches := []Skill{}
		for _, skill := range c.Skills {
			if skill.Name == name {
				matches = append(matches, skill)
			}
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("skill %q has %d matches in source; use a direct local path for ambiguous names", name, len(matches))
		}
		chosen = append(chosen, matches[0])
	}
	return chosen, nil
}

// GitHub tree URLs retain their source form for future ref-aware updates.
func repositoryURL(value string) (remote, ref, path string, err error) {
	u, e := url.Parse(value)
	if e != nil {
		return "", "", "", e
	}
	if u.User != nil {
		return "", "", "", errors.New("use Git credential helpers instead of credentials in source URLs")
	}
	if u.Scheme != "https" {
		return value, "", "", nil
	}
	if queryRef := u.Query().Get("ref"); queryRef != "" {
		copy := *u
		copy.RawQuery = ""
		return copy.String(), queryRef, "", nil
	}
	if u.Host == "github.com" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 4 && (parts[2] == "tree" || parts[2] == "blob") {
			remote = "https://github.com/" + parts[0] + "/" + parts[1] + ".git"
			ref = parts[3]
			path = strings.TrimSuffix(strings.Join(parts[4:], "/"), "/SKILL.md")
			if path == ".." || strings.HasPrefix(path, "../") || strings.Contains(path, "/../") {
				return "", "", "", errors.New("invalid repository subpath")
			}
			return remote, ref, path, nil
		}
	}
	return value, "", "", nil
}

func sourceGit(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.WaitDelay = time.Second
	cancelSourceProcess(cmd)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("fetch source: %w\n%s", err, Clean(string(out)))
	}
	return nil
}

// Pi's HTTPS Git shorthand carries an optional tag after the repository path.
func piGitSource(value string) (string, error) {
	spec := strings.TrimPrefix(value, "git:")
	if strings.HasPrefix(spec, "git@") || strings.HasPrefix(spec, "ssh:") {
		return "", errors.New("use an HTTPS repository URL or an existing local checkout for this Git package")
	}
	if !strings.Contains(spec, "://") {
		spec = "https://" + spec
	}
	u, err := url.Parse(spec)
	if err != nil {
		return "", err
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", errors.New("Git package requires an HTTPS repository URL")
	}
	if i := strings.LastIndex(u.Path, "@"); i >= 0 {
		ref := u.Path[i+1:]
		if ref == "" {
			return "", errors.New("Git package ref is empty")
		}
		u.Path = u.Path[:i]
		q := u.Query()
		q.Set("ref", ref)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}
