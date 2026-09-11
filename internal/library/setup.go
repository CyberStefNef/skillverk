package library

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

type SetupItem struct {
	Managers     []ManagerRecord `json:"managers,omitempty"`
	ManagerIssue string          `json:"manager_issue,omitempty"`
	Requirements Requirements    `json:"requirements,omitempty"`
	Siblings     []string        `json:"sibling_references,omitempty"`
	Content      string          `json:"content,omitempty"`
	Conflict     string          `json:"conflict,omitempty"`
	Name         string          `json:"name"`
	Path         string          `json:"path"`
	Repo         string          `json:"repository,omitempty"`
	Scope        string          `json:"scope"`
	Owner        string          `json:"agent"`
	Tracked      bool            `json:"tracked,omitempty"`
	Digest       string          `json:"digest"`
	Action       string          `json:"action"` // keep, share, replace-shared, remove-broken
	Reason       string          `json:"reason"`
}
type SetupPlan struct {
	Snapshot     string      `json:"snapshot"`
	Format       string      `json:"format"`
	Library      string      `json:"library"`
	Roots        []string    `json:"roots"`
	Repositories []string    `json:"repositories"`
	Items        []SetupItem `json:"items"`
	Warnings     []string    `json:"warnings,omitempty"`
}

func DefaultSetupRoots() []string {
	home, _ := os.UserHomeDir()
	var roots []string
	for _, n := range []string{"work", "personal", "code", "projects"} {
		p := filepath.Join(home, n)
		if i, e := os.Stat(p); e == nil && i.IsDir() {
			roots = append(roots, p)
		}
	}
	return roots
}

// ScanSetup observes only native installations. References, caches, examples,
// plugin-managed skills and Codex's system skills are not cleanup candidates.
func (s *Store) ScanSetup(ctx context.Context, roots []string) (SetupPlan, error) {
	u, e := s.setupLock(ctx)
	if e != nil {
		return SetupPlan{}, e
	}
	defer u()
	if e = s.finishManagerTransfer(false); e != nil {
		return SetupPlan{}, e
	}
	return s.scanSetup(ctx, roots)
}
func (s *Store) scanSetup(ctx context.Context, roots []string) (SetupPlan, error) {
	plan := SetupPlan{Format: "skillverk-setup-1", Library: s.Root}
	if err := ctx.Err(); err != nil {
		return plan, err
	}
	repos := map[string]bool{}
	for _, root := range roots {
		p, e := filepath.Abs(Expand(root))
		if e != nil {
			return plan, e
		}
		p, e = filepath.EvalSymlinks(p)
		if e != nil {
			return plan, e
		}
		if slices.Contains(plan.Roots, p) {
			continue
		}
		plan.Roots = append(plan.Roots, p)
		e = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				plan.Warnings = append(plan.Warnings, path+": "+err.Error())
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if path != p && slices.Contains([]string{".git", ".repos", "node_modules", "vendor", ".venv", "venv", ".uv-cache", ".cache"}, d.Name()) {
				return filepath.SkipDir
			}
			if samePath(path, s.Root) {
				return filepath.SkipDir
			}
			if Exists(filepath.Join(path, ".git")) {
				repos[path] = true
			}
			return nil
		})
		if e != nil {
			return plan, e
		}
	}
	var candidates []string
	for r := range repos {
		candidates = append(candidates, r)
	}
	slices.Sort(candidates)
	seen := map[string]bool{}
	repositoryValues := map[string]any{}
	contexts := append([]string{""}, candidates...)
	for _, repo := range contexts {
		if ctx.Err() != nil {
			return plan, ctx.Err()
		}
		for _, config := range ecosystemConfigs(repo) {
			if raw, e := os.ReadFile(config.Path); e == nil {
				repositoryValues["config:"+config.Path] = bytesDigest(raw)
			}
		}
		rows, e := s.catalogContext(ctx, repo)
		if e != nil {
			plan.Warnings = append(plan.Warnings, repo+": "+e.Error())
			continue
		}
		if repo != "" {
			// Only include repositories whose inventory and validation state were read.
			// A stale .git entry must not turn an earlier warning into a fatal rescan.
			st, err := ReadState(repo)
			if err != nil {
				plan.Warnings = append(plan.Warnings, repo+": "+err.Error())
				continue
			}
			index, err := gitAt(repo, "ls-files", "--stage", "-z")
			if err != nil {
				plan.Warnings = append(plan.Warnings, repo+": "+err.Error())
				continue
			}
			plan.Repositories = append(plan.Repositories, repo)
			repositoryValues["state:"+repo] = st
			repositoryValues["index:"+repo] = index
		}
		for _, sk := range rows {
			if ctx.Err() != nil {
				return plan, ctx.Err()
			}
			if sk.Problem != "" && len(sk.Installations) == 0 {
				plan.Warnings = append(plan.Warnings, sk.Path+": "+sk.Problem)
			}
			for _, in := range sk.Installations {
				if (repo == "" && in.Scope != "global") || (repo != "" && in.Scope != "project") || in.Owner == "admin" || strings.Contains(filepath.ToSlash(in.Path), "/.system/") || seen[in.Path] {
					continue
				}
				if Exists(filepath.Join(in.Path, ".skillverk-owned")) {
					continue
				}
				seen[in.Path] = true
				item := SetupItem{Name: sk.Name, Path: in.Path, Repo: repo, Scope: in.Scope, Owner: in.Owner, Tracked: in.Tracked, Action: "keep"}
				hash, e := setupDigest(ctx, in.Path)
				if e != nil {
					plan.Warnings = append(plan.Warnings, in.Path+": "+e.Error())
					continue
				}
				item.Digest = hash
				if _, e = ReadSkill(in.Path); e != nil {
					info, le := os.Lstat(in.Path)
					_, se := os.Stat(in.Path)
					if le == nil && info.Mode()&os.ModeSymlink != 0 && errors.Is(se, os.ErrNotExist) {
						item.Action = "remove-broken"
						item.Reason = "Broken link; remove link only"
					} else {
						item.Reason = "Invalid installation; preserved: " + e.Error()
					}
				} else if in.Scope == "project" {
					item.Reason = "Repository-owned by default; keep with the code and Git history"
				} else {
					item.Action = "share"
					item.Reason = "Global skill: centralize and preserve availability at the same native path"
				}
				if resolved, err := filepath.EvalSymlinks(in.Path); err == nil {
					item.Content, err = portableDigest(ctx, resolved)
					if err != nil {
						return plan, err
					}
				}
				if item.Content != "" {
					refs, err := siblingReferencesContext(ctx, in.Path)
					if err != nil {
						plan.Warnings = append(plan.Warnings, in.Path+": "+err.Error())
					} else {
						item.Siblings = refs
					}
				}
				if item.Content != "" && item.Action != "remove-broken" {
					item.Requirements, e = inspectRequirements(ctx, in.Path, item.Name)
					if e != nil {
						return plan, e
					}
					if e = item.Requirements.check(item.Name, in.Path); e != nil {
						item.Action = "keep"
						item.Reason = e.Error()
					}
				}
				item.Managers, e = managerRecords(repo, in.Path, item.Name)
				if e != nil {
					item.ManagerIssue = e.Error()
					item.Action = "keep"
					item.Reason = e.Error()
				}
				plan.Items = append(plan.Items, item)
			}
		}
	}
	slices.SortFunc(plan.Items, func(a, b SetupItem) int { return strings.Compare(a.Path, b.Path) })
	slices.Sort(plan.Roots)
	snapshot, err := s.setupSnapshot(ctx, repositoryValues)
	if err != nil {
		return plan, err
	}
	plan.Snapshot = snapshot
	for n := range plan.Items {
		i := &plan.Items[n]
		if i.Content == "" {
			continue
		}
		for _, other := range plan.Items {
			if i.Name == other.Name && i.Path != other.Path && other.Content != "" && i.Content != other.Content {
				i.Conflict += "Different version: " + other.Path + "\n"
			}
		}
		if path, err := s.Path(i.Name); err == nil {
			hash, err := digestContext(ctx, path)
			if err != nil {
				return plan, err
			}
			if hash != i.Content {
				i.Conflict += "Different central version: " + path + "\n"
			}
		}
	}
	return plan, nil
}

// ApplySetup validates the complete approved snapshot before touching any skill.
// A changed source or edited identity requires a fresh review.
func (s *Store) ApplySetup(ctx context.Context, plan SetupPlan, confirmed bool) (results []Result, resultErr error) {
	u, e := s.setupLock(ctx)
	if e != nil {
		return nil, e
	}
	defer u()
	if e = s.finishManagerTransfer(false); e != nil {
		return nil, e
	}
	if !confirmed {
		return nil, errors.New("review and confirm the cleanup plan first")
	}
	if plan.Format != "skillverk-setup-1" || !samePath(plan.Library, s.Root) {
		return nil, errors.New("plan belongs to another library or format")
	}
	fresh, e := s.scanSetup(ctx, plan.Roots)
	if e != nil {
		return nil, e
	}
	if fresh.Snapshot != plan.Snapshot || !reflect.DeepEqual(fresh.Repositories, plan.Repositories) || len(fresh.Items) != len(plan.Items) {
		return nil, errors.New("setup state changed; rescan before applying")
	}
	by := map[string]SetupItem{}
	for _, i := range fresh.Items {
		by[i.Path] = i
	}
	seen := map[string]bool{}
	for _, i := range plan.Items {
		if !slices.Contains([]string{"keep", "share", "replace-shared", "remove-broken"}, i.Action) {
			return nil, fmt.Errorf("unknown action %q", i.Action)
		}
		if seen[i.Path] {
			return nil, fmt.Errorf("duplicate plan path %s", i.Path)
		}
		seen[i.Path] = true
		f, ok := by[i.Path]
		if !ok || f.Name != i.Name || f.Repo != i.Repo || f.Scope != i.Scope || f.Owner != i.Owner || f.Digest != i.Digest || f.Tracked != i.Tracked || f.Content != i.Content || f.Conflict != i.Conflict || !slices.Equal(f.Siblings, i.Siblings) || !reflect.DeepEqual(f.Requirements, i.Requirements) || !reflect.DeepEqual(f.Managers, i.Managers) || f.ManagerIssue != i.ManagerIssue {
			return nil, fmt.Errorf("installation changed; rescan before applying: %s", i.Path)
		}
		if i.Action != "keep" && i.ManagerIssue != "" {
			return nil, errors.New(i.ManagerIssue)
		}
		if i.Action == "remove-broken" && f.Action != "remove-broken" {
			return nil, fmt.Errorf("path is not a broken link: %s", i.Path)
		}
	}
	// Reject conflicting selections before any migration starts.
	selected := map[string]SetupItem{}
	for _, i := range plan.Items {
		if i.Action != "share" && i.Action != "replace-shared" {
			continue
		}
		if i.ManagerIssue != "" {
			return nil, errors.New(i.ManagerIssue)
		}
		if e := i.Requirements.check(i.Name, i.Path); e != nil {
			return nil, e
		}
		if old, ok := selected[i.Name]; ok && old.Content != i.Content {
			return nil, fmt.Errorf("conflicting versions selected: %s and %s; keep one version in place", old.Path, i.Path)
		}
		selected[i.Name] = i
		if p, err := s.Path(i.Name); err == nil {
			h, err := digestContext(ctx, p)
			if err != nil {
				return nil, err
			}
			if h != i.Content && i.Action != "replace-shared" {
				return nil, &ConflictError{Name: i.Name, Existing: p, Incoming: i.Path}
			}
		}
	}
	plan.Items = slices.Clone(plan.Items)
	slices.SortFunc(plan.Items, func(a, b SetupItem) int { return strings.Compare(a.Path, b.Path) })
	// Import every approved sibling before moving any original. A failed import
	// leaves all originals in place, while successful library copies remain usable.
	for _, i := range plan.Items {
		if i.Action != "share" && i.Action != "replace-shared" {
			continue
		}
		for _, ref := range i.Siblings {
			name := siblingName(ref)
			if _, ok := selected[name]; !ok {
				if _, e := s.Path(name); e != nil {
					return nil, fmt.Errorf("%s needs sibling skill %s; select it for sharing or import it first", i.Name, name)
				}
			}
		}
	}
	journal, err := s.beginManagerTransfer(plan.Items)
	if journal != "" {
		defer func() { resultErr = errors.Join(resultErr, s.finishManagerTransfer(resultErr == nil)) }()
	}
	if err != nil {
		return nil, err
	}
	imported := map[string]bool{}
	var importOrder []string
	for _, i := range plan.Items {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		if (i.Action != "share" && i.Action != "replace-shared") || imported[i.Name] {
			continue
		}
		c, e := OpenCollection(i.Path)
		if e != nil {
			return results, e
		}
		_, e = s.publishAdoption(c, i.Action == "replace-shared")
		c.Close()
		if e != nil {
			return results, e
		}
		if e = s.recordManagerSource(i); e != nil {
			return results, e
		}
		imported[i.Name] = true
		importOrder = append(importOrder, i.Name)
		results = append(results, Result{Name: i.Name, Action: "imported; originals retained until verification"})
	}
	for _, name := range importOrder {
		if e := s.VerifySharedReferences(name); e != nil {
			return results, e
		}
	}
	var failures []error
	// Group project copies so agent aliases migrate together.
	groups := map[string][]SetupItem{}
	var order []string
	for _, i := range plan.Items {
		if i.Action == "keep" {
			results = append(results, Result{Name: i.Name, Path: i.Path, Action: "kept in place"})
			continue
		}
		k := i.Repo + "\x00" + i.Name + "\x00" + i.Action
		if i.Repo != "" && (!slices.Contains(DefaultAgents, i.Owner) || i.Path != LinkPath(i.Repo, i.Owner, i.Name)) {
			k += "\x00" + i.Path
		}
		if len(groups[k]) == 0 {
			order = append(order, k)
		}
		groups[k] = append(groups[k], i)
	}
	for _, k := range order {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		items := groups[k]
		i := items[0]
		var rs []Result
		var err error
		switch {
		case i.Action == "remove-broken":
			for _, x := range items {
				actual, er := setupDigest(ctx, x.Path)
				if er == nil && actual != x.Digest {
					er = errors.New("link changed; preserved")
				}
				restore := func() error { return nil }
				if er == nil && x.Tracked {
					restore, er = untrackOriginal(x.Repo, x.Path)
				}
				if er == nil {
					er = os.Remove(x.Path)
					if er != nil {
						er = errors.Join(er, restore())
					}
				}
				r := Result{Name: x.Name, Path: x.Path, Action: "broken link removed"}
				if er != nil {
					r.Action = "preserved"
					r.Error = er.Error()
				}
				rs = append(rs, r)
			}
			err = ResultsError(rs)
		case i.Repo != "" && (!slices.Contains(DefaultAgents, i.Owner) || i.Path != LinkPath(i.Repo, i.Owner, i.Name)):
			rs, err = s.migrateNative(i)
		case i.Scope == "global":
			for _, x := range items {
				more, er := s.ShareGlobal(x.Path, x.Action == "replace-shared")
				rs = append(rs, more...)
				err = errors.Join(err, er)
			}
		default:
			var paths []string
			for _, x := range items {
				paths = append(paths, x.Path)
			}
			rs, err = s.Migrate(i.Repo, paths, i.Action == "replace-shared", true)
		}
		results = append(results, rs...)
		if err != nil {
			failures = append(failures, err)
		}
	}
	return results, errors.Join(failures...)
}

func (s *Store) SetupReviewed() bool { return Exists(filepath.Join(s.Root, "setup-reviewed")) }
func (s *Store) MarkSetupReviewed() error {
	return atomicWrite(filepath.Join(s.Root, "setup-reviewed"), []byte("1\n"), 0600)
}
func SaveSetupPlan(path string, plan SetupPlan) error { return writeJSON(path, plan) }
func ReadSetupPlan(path string) (SetupPlan, error) {
	var p SetupPlan
	e := readJSON(path, &p)
	return p, e
}

// Snapshot includes central content, repository selections and the Git index.
func (s *Store) setupSnapshot(ctx context.Context, repositoryValues map[string]any) (string, error) {
	u, e := s.lock()
	if e != nil {
		return "", e
	}
	defer u()
	d, e := s.load()
	if e != nil {
		return "", e
	}
	values := map[string]any{"database": d}
	for n, entry := range d.Entries {
		h, e := digestContext(ctx, s.entryPath(entry))
		if e != nil {
			return "", e
		}
		values["content:"+n] = h
	}
	for k, v := range repositoryValues {
		values[k] = v
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	b, e := json.Marshal(values)
	return fmt.Sprintf("%x", sha256.Sum256(b)), e
}
func setupDigest(ctx context.Context, path string) (string, error) {
	identity, e := digestContext(ctx, path)
	if e != nil {
		return "", e
	}
	target, e := filepath.EvalSymlinks(path)
	if errors.Is(e, os.ErrNotExist) {
		return identity, nil
	}
	if e != nil {
		return "", e
	}
	content, e := digestContext(ctx, target)
	return identity + ":" + content, e
}
func (p SetupPlan) Summary() string {
	var b strings.Builder
	b.WriteString("Review setup cleanup\n\n")
	for _, i := range p.Items {
		fmt.Fprintf(&b, "%s: %s\n%s\n", i.ActionLabel(), i.Name, i.Path)
		b.WriteString(i.ManagerPreview())
		for _, note := range i.Requirements.Notes {
			fmt.Fprintln(&b, note)
		}
		if i.Tracked && i.Action != "keep" {
			b.WriteString("Remove Git tracking and stage deletion.\n")
		}
		b.WriteString(i.Conflict)
		b.WriteString("\n")
	}
	for _, w := range p.Warnings {
		fmt.Fprintf(&b, "Warning: %s\n", w)
	}
	b.WriteString("Moving a skill removes its original copy after the library copy and agent links are verified. Global skills remain available across repositories. Replacing a library copy updates every repository that uses it. Declining leaves originals and Git tracking untouched.")
	return b.String()
}

func (i SetupItem) ActionLabel() string {
	switch i.Action {
	case "keep":
		return "Keep in place"
	case "share":
		return "Move to library"
	case "replace-shared":
		return "Replace library copy"
	case "remove-broken":
		return "Remove broken link"
	default:
		return i.Action
	}
}

func (s *Store) SetupGlobalDirectories() []string {
	var paths []string
	for _, r := range s.roots("") {
		if r.Scope == "global" && r.Owner != "admin" {
			paths = append(paths, r.Path)
		}
	}
	return paths
}
