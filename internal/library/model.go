// Package library implements the shared library and immediate native exposure.
package library

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"unicode"
)

var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type Skill struct {
	descriptionCharacters int
	CompatibilityPaths    map[string][]string `json:"compatibility_paths,omitempty" yaml:"-"`
	Storage               string              `json:"ownership" yaml:"-"`
	Name                  string              `json:"name" yaml:"name"`
	Description           string              `json:"description" yaml:"description"`
	Path                  string              `json:"path" yaml:"-"`
	Entry                 *Entry              `json:"entry,omitempty" yaml:"-"`
	Selected              bool                `json:"selected" yaml:"-"`
	States                map[string]string   `json:"agents,omitempty" yaml:"-"`
	Harnesses             []string            `json:"harnesses,omitempty" yaml:"-"` // the harnesses a selected skill reaches
	Problem               string              `json:"problem,omitempty" yaml:"-"`
	Installations         []Installation      `json:"installations,omitempty" yaml:"-"`
}
type Entry struct {
	Upstream     string       `json:"upstream,omitempty"`
	UpstreamPath string       `json:"upstream_path,omitempty"`
	Requirements Requirements `json:"requirements,omitempty"`
	Siblings     []string     `json:"sibling_references,omitempty"`
	ID           string       `json:"id"`
	Source       string       `json:"source"`
	Subpath      string       `json:"subpath"`
	// Digest is the content as it arrived from the source. Entries written
	// before this was recorded have none, and comparisons fall back to saying
	// only that two sides differ.
	Digest string `json:"digest,omitempty"`
}
type State struct {
	AdoptionReviewed bool              `json:"adoption_reviewed,omitempty"`
	Format           string            `json:"format"`
	Library          string            `json:"library"`
	Context          string            `json:"context"`
	Selected         map[string]string `json:"selected"` // name -> entry lifetime, not a content version
	// Harnesses narrows a selected skill to some of the enabled harnesses.
	// A skill with no entry here follows Agents, which is the common case, so
	// the map stays empty until someone asks for something narrower.
	Harnesses map[string][]string `json:"harnesses,omitempty"` // name -> subset of Agents
	Agents    []string            `json:"agents"`
	Errors    map[string]string   `json:"errors,omitempty"`
}

// Links reports whether a selected skill reaches this harness. Absent from
// Harnesses means "every harness the repository has enabled", so the narrower
// setting is opt-in and nothing has to be written for the ordinary case.
func (st State) Links(name, agent string) bool {
	if !slices.Contains(st.Agents, agent) {
		return false
	}
	chosen, narrowed := st.Harnesses[name]
	return !narrowed || slices.Contains(chosen, agent)
}

// SkillAgents lists the harnesses a selected skill reaches, in registry order.
func (st State) SkillAgents(name string) []string {
	var out []string
	for _, agent := range st.Agents {
		if st.Links(name, agent) {
			out = append(out, agent)
		}
	}
	return out
}

type Activation struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	Repo     string `json:"repo"`
	Metadata string `json:"metadata"`
	Context  string `json:"context"`
	Agent    string `json:"agent"`
	Path     string `json:"path"`
	Target   string `json:"target"`
	Created  bool   `json:"created"`
	Pending  bool   `json:"pending"`
}
type Original struct {
	ContentDigest string `json:"content_digest"`
	EntryID       string `json:"entry_id"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	Backup        string `json:"backup,omitempty"`
	Digest        string `json:"digest"`
	Scope         string `json:"scope"`
	Repo          string `json:"repo,omitempty"`
	Context       string `json:"context,omitempty"`
}
type database struct {
	Removing  map[string]string `json:"removing,omitempty"`
	Format    string            `json:"format"`
	Entries   map[string]Entry  `json:"entries"`
	Links     []Activation      `json:"links"`
	Originals []Original        `json:"originals,omitempty"`
}
type Store struct {
	Root      string
	ScanRoots []ScanRoot
}
type Result struct {
	Name   string `json:"name,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Path   string `json:"path,omitempty"`
	Action string `json:"action"`
	Error  string `json:"error,omitempty"`
}

func ResultsError(rs []Result) error {
	var es []error
	for _, r := range rs {
		if r.Error != "" {
			es = append(es, fmt.Errorf("%s %s %s: %s", r.Name, r.Agent, r.Path, r.Error))
		}
	}
	return errors.Join(es...)
}
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, s)
}
func ValidateName(n string) error {
	if len(n) > 64 || !namePattern.MatchString(n) {
		return fmt.Errorf("invalid skill name %q: use lowercase words separated by hyphens, up to 64 characters", n)
	}
	return nil
}
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	return p
}
func Exists(p string) bool {
	_, e := os.Lstat(p)
	return !errors.Is(e, os.ErrNotExist) && !errors.Is(e, syscall.ENOTDIR)
}
func within(p, root string) bool {
	rel, e := filepath.Rel(root, p)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func newID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func New(root string) (*Store, error) {
	if root == "" {
		root = os.Getenv("SKILLVERK_HOME")
	}
	if root == "" {
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			h, e := os.UserHomeDir()
			if e != nil {
				return nil, e
			}
			base = filepath.Join(h, ".local", "share")
			if runtime.GOOS == "windows" {
				if local := os.Getenv("LOCALAPPDATA"); local != "" {
					base = local
				} else {
					base = filepath.Join(h, "AppData", "Local")
				}
			}
		}
		root = filepath.Join(base, "skillverk")
	}
	root, e := filepath.Abs(Expand(root))
	if e != nil {
		return nil, e
	}
	for _, old := range []string{"versions", "packages.json", "skills"} {
		if Exists(filepath.Join(root, old)) {
			return nil, fmt.Errorf("obsolete Skillverk storage at %s; use a fresh library and import/activate again", root)
		}
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return nil, e
	}
	if repo, e := Project(root); e != nil {
		return nil, e
	} else if repo != "" {
		return nil, fmt.Errorf("library must be outside Git working trees: %s", root)
	}
	for _, part := range []string{string(filepath.Separator) + ".agents" + string(filepath.Separator) + "skills", string(filepath.Separator) + ".claude" + string(filepath.Separator) + "skills", string(filepath.Separator) + ".codex" + string(filepath.Separator) + "skills"} {
		if strings.Contains(root+string(filepath.Separator), part+string(filepath.Separator)) {
			return nil, errors.New("library must be outside agent discovery roots")
		}
	}
	for _, r := range ecosystemRoots("") {
		if within(root, r.Path) {
			return nil, errors.New("library must be outside agent discovery roots")
		}
	}
	return &Store{Root: root}, nil
}
func (s *Store) SkillsPath() string       { return filepath.Join(s.Root, "content") }
func (s *Store) entryPath(e Entry) string { return filepath.Join(s.SkillsPath(), e.ID) }
func LinkPath(repo, agent, name string) string {
	return filepath.Join(repo, filepath.FromSlash(agentPaths[agent]), name)
}
func gitAt(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	out, e := cmd.CombinedOutput()
	if e != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), e, Clean(strings.TrimSpace(string(out))))
	}
	return strings.TrimSpace(string(out)), nil
}
func gitEnv() []string {
	var env []string
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GIT_") {
			env = append(env, v)
		}
	}
	return env
}

// Project asks Git at each enclosing .git boundary, including nested repositories.
func Project(path string) (string, error) {
	if path == "" {
		var e error
		path, e = os.Getwd()
		if e != nil {
			return "", e
		}
	}
	path, e := filepath.Abs(Expand(path))
	if e != nil {
		return "", e
	}
	path, e = filepath.EvalSymlinks(path)
	if e != nil {
		return "", e
	}
	i, e := os.Stat(path)
	if e != nil {
		return "", e
	}
	if !i.IsDir() {
		return "", errors.New("context must be a directory")
	}
	out, e := gitAt(path, "rev-parse", "--show-toplevel")
	if e != nil {
		if strings.Contains(e.Error(), "not a git repository") {
			return "", nil
		}
		return "", e
	}
	return filepath.Clean(out), nil
}
func metadata(repo string) (string, error) {
	if repo == "" {
		return "", errors.New("activation requires a Git working tree")
	}
	root, e := Project(repo)
	if e != nil {
		return "", e
	}
	if root != repo {
		return "", fmt.Errorf("working tree unavailable or moved: %s", repo)
	}
	return gitAt(repo, "rev-parse", "--absolute-git-dir")
}
func statePath(meta string) string { return filepath.Join(meta, "skillverk-state.json") }
func readJSON(path string, v any) error {
	raw, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(raw, v); e != nil {
		return fmt.Errorf("%s: %w", path, e)
	}
	return nil
}
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return atomicWrite(path, append(b, '\n'), 0600)
}
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".skillverk-write-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	return replaceFile(f.Name(), path)
}
func (s *Store) load() (database, error) {
	if e := s.recoverContent(); e != nil {
		return database{}, e
	}
	d := database{Format: "skillverk-library-1", Entries: map[string]Entry{}}
	e := readJSON(filepath.Join(s.Root, "library.json"), &d)
	if errors.Is(e, os.ErrNotExist) {
		e = nil
	}
	if e != nil {
		return d, e
	}
	if d.Format != "skillverk-library-1" || d.Entries == nil {
		return d, errors.New("unsupported library format; fresh import required")
	}
	if d.Removing == nil {
		d.Removing = map[string]string{}
	}
	for id, n := range d.Removing {
		if !validID(id) || ValidateName(n) != nil {
			return d, errors.New("invalid pending content deletion")
		}
	}
	for n, x := range d.Entries {
		for _, ref := range x.Siblings {
			if !strings.HasPrefix(ref, "../") || strings.HasPrefix(ref, "../../") || filepath.ToSlash(filepath.Clean(ref)) != ref || ValidateName(siblingName(ref)) != nil {
				return d, errors.New("invalid sibling reference")
			}
		}
		if ValidateName(n) != nil || !validID(x.ID) {
			return d, errors.New("invalid library entry")
		}
	}
	for _, a := range d.Links {
		if a.Agent == "global" && a.Repo == "" && a.Metadata == "" && filepath.IsAbs(a.Path) && filepath.Dir(a.Path) != a.Path && ValidateName(a.Name) == nil && validID(a.ID) && (a.Target == s.entryPath(Entry{ID: a.ID}) || a.Target == filepath.Join(s.entryPath(Entry{ID: a.ID}), "SKILL.md")) {
			continue
		}
		if ValidateName(a.Name) != nil || !validID(a.ID) || !slices.Contains(Agents, a.Agent) || a.Path != LinkPath(a.Repo, a.Agent, a.Name) || a.Target != s.entryPath(Entry{ID: a.ID}) || !filepath.IsAbs(a.Metadata) {
			return d, errors.New("invalid activation index")
		}
	}
	return d, nil
}
func validID(s string) bool            { b, e := hex.DecodeString(s); return e == nil && len(b) == 16 }
func (s *Store) save(d database) error { return writeJSON(filepath.Join(s.Root, "library.json"), d) }
func ReadState(repo string) (State, error) {
	st := State{Format: "skillverk-worktree-1", Selected: map[string]string{}, Agents: slices.Clone(DefaultAgents), Errors: map[string]string{}}
	if repo == "" {
		return st, nil
	}
	meta, e := metadata(repo)
	if e != nil {
		return st, e
	}
	e = readJSON(statePath(meta), &st)
	if errors.Is(e, os.ErrNotExist) {
		return st, nil
	}
	if e != nil {
		return st, e
	}
	if st.Format != "skillverk-worktree-1" || st.Selected == nil || len(st.Agents) == 0 {
		return st, errors.New("invalid private worktree state")
	}
	for n, id := range st.Selected {
		if ValidateName(n) != nil || !validID(id) {
			return st, errors.New("invalid selection")
		}
	}
	for _, a := range st.Agents {
		if !slices.Contains(Agents, a) {
			return st, errors.New("invalid agent setting")
		}
	}
	for n, agents := range st.Harnesses {
		if ValidateName(n) != nil || len(agents) == 0 {
			return st, errors.New("invalid harness selection")
		}
		for _, a := range agents {
			if !slices.Contains(Agents, a) {
				return st, errors.New("invalid harness selection")
			}
		}
	}
	if st.Errors == nil {
		st.Errors = map[string]string{}
	}
	return st, nil
}
func (s *Store) state(repo string) (State, string, error) {
	meta, e := metadata(repo)
	if e != nil {
		return State{}, "", e
	}
	st, e := ReadState(repo)
	if e != nil {
		return st, meta, e
	}
	if st.Library != "" && !samePath(st.Library, s.Root) {
		return st, meta, fmt.Errorf("working tree belongs to library %s; use --home with that library", st.Library)
	}
	st.Library = s.Root
	if st.Context == "" {
		st.Context = newID()
	}
	return st, meta, nil
}
func (s *Store) lock() (func(), error) { return fileLock(filepath.Join(s.Root, "library.lock")) }
