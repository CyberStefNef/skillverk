package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

type ScanRoot struct{ Path, Scope, Owner string }
type Installation struct {
	Tracked bool   `json:"tracked,omitempty"`
	Path    string `json:"path"`
	Scope   string `json:"scope"`
	Owner   string `json:"owner"`
}

func (s *Store) roots(repo string) []ScanRoot {
	var roots []ScanRoot
	if repo != "" {
		for _, a := range Agents {
			roots = append(roots, ScanRoot{filepath.Join(repo, filepath.FromSlash(agentPaths[a])), "project", a})
		}
	}
	if s.ScanRoots != nil {
		return append(roots, s.ScanRoots...)
	}
	h, _ := os.UserHomeDir()
	codex := os.Getenv("CODEX_HOME")
	if codex == "" {
		codex = filepath.Join(h, ".codex")
	}
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = filepath.Join(h, ".claude")
	}
	for _, r := range []ScanRoot{{filepath.Join(h, ".openclaw", "skills"), "global", "shared"}, {filepath.Join(h, ".agents", "skills"), "global", "shared"}, {filepath.Join(h, ".config", "agents", "skills"), "global", "shared"}, {filepath.Join(codex, "skills"), "global", "codex"}, {filepath.Join(claude, "skills"), "global", "claude"}} {
		roots = append(roots, r)
	}
	if filepath.Separator == '/' {
		roots = append(roots, ScanRoot{"/etc/codex/skills", "global", "admin"})
	}
	for _, base := range []string{codex, claude} {
		matches, _ := filepath.Glob(filepath.Join(base, "plugins", "cache", "*", "*", "*", "skills"))
		for _, p := range matches {
			roots = append(roots, ScanRoot{p, "plugin", "plugin manager"})
		}
	}
	// Ancestor native skills are inherited observations, not repository selection.
	if repo != "" {
		for p := filepath.Dir(repo); p != filepath.Dir(p); p = filepath.Dir(p) {
			for _, a := range Agents {
				roots = append(roots, ScanRoot{filepath.Join(p, filepath.FromSlash(agentPaths[a])), "inherited", a})
			}
		}
	}
	roots = append(roots, ecosystemRoots(repo)...)
	return roots
}
func (s *Store) Catalog(repo string) ([]Skill, error) {
	return s.catalogContext(context.Background(), repo)
}
func (s *Store) catalogContext(ctx context.Context, repo string) ([]Skill, error) {
	u, e := s.lock()
	if e != nil {
		return nil, e
	}
	defer u()
	d, e := s.load()
	if e != nil {
		return nil, e
	}
	st, e := ReadState(repo)
	if e != nil {
		return nil, e
	}
	if st.Library != "" && !samePath(st.Library, s.Root) {
		return nil, fmt.Errorf("working tree uses library %s", st.Library)
	}
	by := map[string]Skill{}
	for n, entry := range d.Entries {
		sk, err := ReadSkill(s.entryPath(entry))
		if err != nil {
			sk = Skill{Name: n, Path: s.entryPath(entry), Problem: err.Error()}
		}
		if sk.Name != n {
			sk.Problem = "central skill name differs from library name"
			sk.Name = n
		}
		for _, issue := range []error{s.verifySiblings(d, entry), s.verifyRequirements(n, entry)} {
			if issue != nil {
				if sk.Problem != "" {
					sk.Problem += "\n"
				}
				sk.Problem += issue.Error()
			}
		}
		x := entry
		sk.Entry = &x
		by[n] = sk
	}
	for n := range st.Selected {
		if _, ok := by[n]; !ok {
			by[n] = Skill{Name: n, Problem: "central content missing"}
		}
	}
	var tracked []string
	if repo != "" {
		out, err := gitAt(repo, "ls-files", "-z")
		if err != nil {
			return nil, err
		}
		tracked = strings.Split(out, "\x00")
	}
	roots := s.roots(repo)
	if s.ScanRoots == nil {
		configured, issues := configuredRoots(repo)
		roots = append(roots, configured...)
		nested, err := nestedRoots(ctx, repo)
		roots = append(roots, nested...)
		if err != nil {
			issues = append(issues, err)
		}
		for i, err := range issues {
			name := fmt.Sprintf("[inventory %d]", i)
			by[name] = Skill{Name: name, Problem: err.Error()}
		}
	}
	seenRoots := map[string]bool{}
	seenPaths := map[string]bool{}
	for _, root := range roots {
		if seenRoots[root.Path] {
			continue
		}
		seenRoots[root.Path] = true
		paths, err := installationPaths(ctx, root.Path, root.Owner)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			n := "[" + root.Owner + " inventory]"
			by[n] = Skill{Name: n, Path: root.Path, Problem: err.Error()}
			continue
		}
		for _, path := range paths {
			if seenPaths[path] {
				continue
			}
			seenPaths[path] = true
			owned := false
			for _, a := range d.Links {
				if a.Path == path && a.Created && samePath(linkTarget(path), a.Target) {
					owned = true
					break
				}
			}
			if owned {
				continue
			}
			if !Exists(filepath.Join(path, "SKILL.md")) && linkTarget(path) == "" && !strings.HasSuffix(strings.ToLower(path), ".md") {
				continue
			}
			sk, er := ReadSkill(path)
			if er != nil {
				sk = Skill{Name: filepath.Base(path), Path: path, Problem: er.Error()}
			}
			existing, ok := by[sk.Name]
			if !ok {
				existing = sk
			} else if sk.Problem != "" {
				if existing.Problem != "" {
					existing.Problem += "\n"
				}
				existing.Problem += sk.Problem
			}
			if root.Scope == "project" && !existing.HasScope("project") {
				existing.Description = sk.Description
			}
			installation := Installation{Path: path, Scope: root.Scope, Owner: root.Owner}
			if root.Scope == "project" {
				rel, _ := filepath.Rel(repo, path)
				rel = filepath.ToSlash(rel)
				for _, p := range tracked {
					if p == rel || strings.HasPrefix(p, rel+"/") {
						installation.Tracked = true
						break
					}
				}
			}
			existing.Installations = append(existing.Installations, installation)
			by[sk.Name] = existing
		}
	}
	var rows []Skill
	for n, sk := range by {
		sk.Selected = st.Selected[n] != ""
		sk.States = map[string]string{}
		sk.CompatibilityPaths = compatibilityPaths(repo, n)
		if repo != "" {
			for _, a := range Agents {
				wanted := sk.Selected && st.Links(n, a)
				path := LinkPath(repo, a, n)
				state := "off"
				idx := findRecord(&d, st, repo, n, a)
				if !slices.Contains(st.Agents, a) && idx < 0 && !Exists(path) && !slices.Contains(DefaultAgents, a) {
					continue
				}
				owned := idx >= 0 && d.Links[idx].Created && samePath(linkTarget(path), d.Links[idx].Target)
				if owned {
					state = "unexpected managed link"
					if wanted {
						state = "active"
						if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
							state = "missing central content"
						}
					}
				} else if wanted {
					state = "missing"
					if Exists(path) {
						state = "conflict (external path preserved)"
					}
				} else if Exists(path) {
					state = "external"
				}
				if er := st.Errors[key(n, a)]; er != "" {
					state = "failed: " + er
				}
				sk.States[a] = state
			}
		}
		if sk.Selected {
			sk.Harnesses = st.SkillAgents(n)
		}
		sk.Storage = sk.Ownership()
		rows = append(rows, sk)
	}
	slices.SortFunc(rows, func(a, b Skill) int {
		ar := a.Selected || a.HasScope("project")
		br := b.Selected || b.HasScope("project")
		if ar != br {
			if ar {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return rows, nil
}
func (s Skill) HasScope(scope string) bool {
	for _, i := range s.Installations {
		if i.Scope == scope {
			return true
		}
	}
	return false
}
func (s Skill) Status() string {
	if s.Selected {
		for _, v := range s.States {
			if v != "active" && v != "off" && v != "external" {
				return "partial"
			}
		}
		return "active"
	}
	for _, v := range s.States {
		if strings.HasPrefix(v, "failed") || v == "unexpected managed link" {
			return "partial"
		}
	}
	if len(s.Installations) > 0 {
		return "external"
	}
	return "available"
}
func (s Skill) Details() string {
	contentLabel := "Content: "
	if s.HasScope("project") && s.Entry != nil {
		contentLabel = "Shared library content (separate from repository copy): "
	}
	lines := []string{s.Name, s.Description, contentLabel + s.Path, "Ownership: " + s.Ownership(), "Status: " + s.Status()}
	if s.Entry != nil {
		lines = append(lines, "Source: "+s.Entry.Source+" / "+s.Entry.Subpath)
		if s.Entry.Upstream != "" {
			lines = append(lines, "Updates from: "+s.Entry.Upstream+" / "+s.Entry.UpstreamPath)
		}
		lines = append(lines, s.Entry.Requirements.Notes...)
	}
	for _, a := range Agents {
		if v := s.States[a]; v != "" {
			lines = append(lines, AgentLabel(a)+" managed link: "+v)
		}
	}
	for _, agent := range Agents {
		for _, path := range s.CompatibilityPaths[agent] {
			lines = append(lines, AgentLabel(agent)+" may also discover: "+path)
		}
	}
	if len(s.CompatibilityPaths) > 0 {
		lines = append(lines, "Harness settings control managed links. Compatibility paths can remain discoverable; native permissions and session state still apply.")
	}
	for _, i := range s.Installations {
		lines = append(lines, i.Scope+" · "+i.Owner+" · "+i.Path)
		if i.Tracked {
			lines = append(lines, "Tracked by Git: migration requires confirmation of complete original removal, including Git tracking.")
		}
	}
	if s.HasScope("plugin") {
		lines = append(lines, "Plugin cache is inventory only; its manager controls enablement.")
	}
	if s.HasScope("global") || s.HasScope("inherited") {
		lines = append(lines, "External availability is independent of this repository's selection.")
	}
	if s.Problem != "" {
		lines = append(lines, s.Problem)
	}
	return Clean(strings.Join(lines, "\n"))
}
func (s *Store) Find(repo, name string) (Skill, error) {
	rows, e := s.Catalog(repo)
	if e != nil {
		return Skill{}, e
	}
	for _, r := range rows {
		if r.Name == name {
			return r, nil
		}
	}
	return Skill{}, fmt.Errorf("unknown skill %q", name)
}
func (s *Store) Doctor(repo string) ([]Result, error) {
	rows, e := s.Catalog(repo)
	if e != nil {
		return nil, e
	}
	var rs []Result
	for _, sk := range rows {
		if sk.Problem != "" {
			rs = append(rs, Result{Name: sk.Name, Path: sk.Path, Action: "inspect", Error: sk.Problem})
		}
		for a, v := range sk.States {
			if v != "active" && v != "off" && v != "external" {
				rs = append(rs, Result{Name: sk.Name, Agent: a, Path: LinkPath(repo, a, sk.Name), Action: "retry", Error: v})
			}
		}
	}
	u, e := s.lock()
	if e != nil {
		return rs, e
	}
	defer u()
	d, e := s.load()
	if e != nil {
		return rs, e
	}
	if Exists(filepath.Join(s.Root, "manager-transfer.json")) {
		rs = append(rs, Result{Action: "finish installer handoff", Error: "installer handoff is incomplete; repair failed links, then run setup again"})
	}
	for id, n := range d.Removing {
		rs = append(rs, Result{Name: n, Path: s.entryPath(Entry{ID: id}), Action: "pending content cleanup", Error: "central directory could not be removed; retry on the next visit"})
	}
	for _, a := range d.Links {
		if a.Pending {
			rs = append(rs, Result{Name: a.Name, Agent: a.Agent, Path: a.Path, Action: "pending cleanup", Error: "visit this working tree to reconcile; original path may be unreachable or replaced"})
		} else if a.Repo == "" && a.Created && !samePath(linkTarget(a.Path), a.Target) {
			rs = append(rs, Result{Name: a.Name, Agent: a.Agent, Path: a.Path, Action: "inspect", Error: "managed global link is missing or replaced; review the installation before sharing it again"})
		}
	}
	return rs, ResultsError(rs)
}
