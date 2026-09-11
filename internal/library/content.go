package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// copyTree materializes package-local links into independent content. This also
// supports ordinary Windows users; escaping links and cycles are rejected.
func copyTree(src, dst string) error {
	root, e := resolvePath(src)
	if e != nil {
		return e
	}
	var copyNode func(string, string, map[string]bool) error
	copyNode = func(from, to string, anc map[string]bool) error {
		resolved, e := resolvePath(from)
		if e != nil {
			return e
		}
		if !within(resolved, root) {
			return fmt.Errorf("resource escapes skill: %s", from)
		}
		i, e := os.Stat(resolved)
		if e != nil {
			return e
		}
		if i.IsDir() {
			if anc[resolved] {
				return fmt.Errorf("resource link cycle: %s", from)
			}
			next := map[string]bool{}
			for k, v := range anc {
				next[k] = v
			}
			next[resolved] = true
			if e = os.MkdirAll(to, 0755); e != nil {
				return e
			}
			entries, e := os.ReadDir(resolved)
			if e != nil {
				return e
			}
			for _, d := range entries {
				if d.Name() == ".git" {
					continue
				}
				if e = copyNode(filepath.Join(resolved, d.Name()), filepath.Join(to, d.Name()), next); e != nil {
					return e
				}
			}
			return nil
		}
		if !i.Mode().IsRegular() {
			return fmt.Errorf("unsupported special resource: %s", from)
		}
		in, e := os.Open(resolved)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, i.Mode().Perm())
		if e != nil {
			return e
		}
		_, e = io.Copy(out, in)
		return errors.Join(e, out.Close())
	}
	return copyNode(root, dst, map[string]bool{})
}
func digest(path string) (string, error) { return digestContext(context.Background(), path) }
func digestContext(ctx context.Context, path string) (string, error) {
	h := sha256.New()
	e := filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, e := filepath.Rel(path, p)
		if e != nil {
			return e
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintln(h, filepath.ToSlash(rel), d.Type(), info.Mode().Perm())
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || linkTarget(p) != "" {
			target, e := os.Readlink(p)
			if e != nil {
				return e
			}
			fmt.Fprintln(h, target)
			return nil
		}
		if !d.Type().IsRegular() {
			return errors.New("unsupported original resource")
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		_, e = io.Copy(h, contextReader{ctx, f})
		return errors.Join(e, f.Close())
	})
	return hex.EncodeToString(h.Sum(nil)), e
}

type ConflictError struct{ Name, Existing, Incoming string }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s already exists\nExisting source: %s\nIncoming source: %s\nExplicit replacement changes shared content in every activated repository.", e.Name, e.Existing, e.Incoming)
}

// Publish refuses every occupied name unless replacement was explicitly approved.
func (s *Store) Publish(c *Collection, names []string, replace bool) (map[string]Entry, error) {
	return s.publish(c, names, replace, true)
}

func (s *Store) publish(c *Collection, names []string, replace, dependencies bool) (map[string]Entry, error) {
	selected, e := c.Select(names)
	if e != nil {
		return nil, e
	}
	unlock, e := s.lock()
	if e != nil {
		return nil, e
	}
	defer unlock()
	d, e := s.load()
	if e != nil {
		return nil, e
	}
	if dependencies {
		selected, e = s.dependencySelection(c, selected, d)
		if e != nil {
			return nil, e
		}
	}
	conflict := &ConflictError{}
	for _, sk := range selected {
		if old, ok := d.Entries[sk.Name]; ok && !replace {
			conflict.Name += sk.Name + " "
			conflict.Existing += sk.Name + ": " + old.Source + " / " + old.Subpath + "; "
			conflict.Incoming += sk.Name + ": " + c.Source + " / " + sk.Path + "; "
		}
		if within(s.Root, sk.Path) || within(sk.Path, s.Root) {
			return nil, errors.New("import source overlaps central storage")
		}
	}
	if conflict.Name != "" {
		return nil, conflict
	}
	result := map[string]Entry{}
	for _, sk := range selected {
		rel, e := filepath.Rel(c.Root, sk.Path)
		if e != nil {
			return result, e
		}
		entry, ok := d.Entries[sk.Name]
		if !ok {
			entry.ID = newID()
		}
		entry.Source = c.Source
		entry.Subpath = filepath.ToSlash(rel)
		if e = s.publishOne(&d, sk, entry); e != nil {
			return result, e
		}
		result[sk.Name] = d.Entries[sk.Name]
	}
	if dependencies {
		for _, sk := range selected {
			if e := s.verifySiblings(d, d.Entries[sk.Name]); e != nil {
				return result, e
			}
		}
	}
	return result, nil
}

// A temporary write-ahead journal restores complete previous content if a
// process stops between directory renames. It is removed after commit; it is
// recovery state, not user-addressable history.
type contentTransaction struct {
	Name       string
	Entry      Entry
	Previous   *Entry
	Stage      string
	HadContent bool
	Committed  bool
}

func (s *Store) publishOne(d *database, sk Skill, entry Entry) error {
	if e := os.MkdirAll(s.SkillsPath(), 0700); e != nil {
		return e
	}
	stage, e := os.MkdirTemp(s.Root, ".import-")
	if e != nil {
		return e
	}
	journaled := false
	defer func() {
		if !journaled {
			_ = os.RemoveAll(stage)
		}
	}()
	if e = copySkill(sk.Path, filepath.Join(stage, "content")); e != nil {
		return e
	}
	check, e := ReadSkill(filepath.Join(stage, "content"))
	if e != nil {
		return e
	}
	if check.Name != sk.Name {
		return errors.New("source name changed during import")
	}
	var known []string
	for name := range d.Entries {
		known = append(known, name)
	}
	refs, e := siblingReferences(sk.Path, known...)
	if e != nil {
		return e
	}
	entry.Siblings = refs
	entry.Requirements, e = inspectRequirements(context.Background(), sk.Path, sk.Name)
	if e != nil {
		return e
	}
	for _, activation := range d.Links {
		if activation.ID == entry.ID && activation.Created && !activation.Pending {
			if err := validateNativeMetadata(sk.Path); err != nil {
				return fmt.Errorf("update would break an active skill; shared content preserved: %w", err)
			}
			if err := entry.Requirements.check(sk.Name, s.entryPath(entry)); err != nil {
				return fmt.Errorf("update would break an active skill; shared content preserved: %w", err)
			}
		}
	}
	for name, dependent := range d.Entries {
		if name == sk.Name {
			continue
		}
		for _, ref := range dependent.Siblings {
			if siblingName(ref) != sk.Name {
				continue
			}
			suffix := strings.TrimPrefix(strings.TrimPrefix(ref, "../"+sk.Name), "/")
			if _, err := os.Stat(filepath.Join(stage, "content", filepath.FromSlash(suffix))); err != nil {
				return fmt.Errorf("update would break %s reference used by %s; shared content preserved: %w", ref, name, err)
			}
		}
	}
	if e = s.prepareSiblingAliases(*d, entry); e != nil {
		return e
	}
	if e = s.checkSiblingAlias(sk.Name, entry); e != nil {
		return e
	}
	target := s.entryPath(entry)
	tx := contentTransaction{Name: sk.Name, Entry: entry, Stage: stage, HadContent: Exists(target)}
	if old, ok := d.Entries[sk.Name]; ok {
		tx.Previous = &old
	}
	journal := filepath.Join(s.Root, "content-transaction.json")
	if e = writeJSON(journal, tx); e != nil {
		return e
	}
	journaled = true
	rollback := func(cause error) error { return errors.Join(cause, s.recoverContent()) }
	if tx.HadContent {
		if e = os.Rename(target, filepath.Join(stage, "previous")); e != nil {
			return rollback(fmt.Errorf("replace shared content (close open handles and retry): %w", e))
		}
	}
	if e = os.Rename(filepath.Join(stage, "content"), target); e != nil {
		return rollback(e)
	}
	// Remember the content as it arrived. Without this the library can only say
	// that a copy and its source disagree; with it, it can say which of the two
	// moved, which is the difference between "out of date" and "you changed it".
	if entry.Digest, e = digest(target); e != nil {
		return rollback(e)
	}
	d.Entries[sk.Name] = entry
	if e = s.save(*d); e != nil {
		return rollback(e)
	}
	if e = s.ensureSiblingAlias(sk.Name, entry); e != nil {
		return rollback(e)
	}
	tx.Committed = true
	if e = writeJSON(journal, tx); e != nil {
		return rollback(e)
	}
	if e = s.recoverContent(); e != nil {
		return e
	}
	journaled = false
	return nil
}
func (s *Store) recoverContent() error {
	path := filepath.Join(s.Root, "content-transaction.json")
	var tx contentTransaction
	if e := readJSON(path, &tx); errors.Is(e, os.ErrNotExist) {
		return nil
	} else if e != nil {
		return e
	}
	if ValidateName(tx.Name) != nil || !validID(tx.Entry.ID) || filepath.Dir(tx.Stage) != s.Root || !strings.HasPrefix(filepath.Base(tx.Stage), ".import-") {
		return errors.New("invalid content recovery journal; preserved for inspection")
	}
	if !tx.Committed {
		target := s.entryPath(tx.Entry)
		backup := filepath.Join(tx.Stage, "previous")
		if Exists(backup) {
			if e := os.RemoveAll(target); e != nil {
				return e
			}
			if e := os.Rename(backup, target); e != nil {
				return fmt.Errorf("previous content retained at %s: %w", backup, e)
			}
		} else if !tx.HadContent {
			if samePath(linkTarget(s.siblingPath(tx.Name)), target) {
				if e := s.removeSiblingAlias(tx.Name, tx.Entry.ID); e != nil {
					return e
				}
			}
			if e := os.RemoveAll(target); e != nil {
				return e
			}
		}
		d := database{Format: "skillverk-library-1", Entries: map[string]Entry{}}
		if e := readJSON(filepath.Join(s.Root, "library.json"), &d); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if tx.Previous != nil {
			d.Entries[tx.Name] = *tx.Previous
		} else {
			delete(d.Entries, tx.Name)
		}
		if e := s.save(d); e != nil {
			return e
		}
	}
	if e := os.RemoveAll(tx.Stage); e != nil {
		return e
	}
	return os.Remove(path)
}

// sourceRef is the place an entry follows for updates: its upstream when it has
// one, and otherwise the source it was imported from.
func sourceRef(e Entry) (string, string) {
	if e.Upstream != "" {
		return e.Upstream, e.UpstreamPath
	}
	return e.Source, e.Subpath
}

// sourceSkillPath locates a skill inside an opened source the way an update
// does: the recorded subpath first, a search by name for the sources that pin
// none, and a final check that the result really sits inside the source.
func sourceSkillPath(c *Collection, entry Entry, name string) (string, error) {
	_, subpath := sourceRef(entry)
	path := filepath.Join(c.Root, filepath.FromSlash(subpath))
	if entry.Upstream != "" && subpath == "" {
		matches, e := c.Select([]string{name})
		if e != nil {
			return "", e
		}
		path = matches[0].Path
	}
	if _, probeErr := ReadSkill(path); probeErr != nil {
		if matches, e := c.Select([]string{name}); e == nil {
			path = matches[0].Path
		}
	}
	resolved, e := resolvePath(path)
	if e != nil {
		return "", e
	}
	if !within(resolved, c.Root) {
		return "", errors.New("path escapes its source")
	}
	return resolved, nil
}

// Candidate is a skill in one of the library's own sources that could be where
// an adopted skill originally came from.
type Candidate struct {
	Upstream     string
	UpstreamPath string
	Name         string
	Description  string
	// Identical means the source content matches the library copy exactly, the
	// only evidence that settles the question. SameDescription is weaker: the
	// same name and description, with a body that has moved on either side.
	Identical       bool
	SameDescription bool
}

// Strength orders candidates by how much they prove, best first.
func (c Candidate) Strength() int {
	switch {
	case c.Identical:
		return 0
	case c.SameDescription:
		return 1
	}
	return 2
}

// Sources lists the remote sources the library already follows, deduplicated.
// These are the only places worth searching for a lost upstream: they are the
// repositories this user actually imports from, so a hit is a plausible origin
// rather than a name collision from the whole internet.
func (s *Store) Sources() ([]string, error) {
	unlock, e := s.lock()
	if e != nil {
		return nil, e
	}
	defer unlock()
	d, e := s.load()
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	var out []string
	for _, entry := range d.Entries {
		source, _ := sourceRef(entry)
		if source == "" || filepath.IsAbs(Expand(source)) || seen[source] {
			continue
		}
		seen[source] = true
		out = append(out, source)
	}
	slices.Sort(out)
	return out, nil
}

// FindUpstream looks through the library's own sources for a skill that could
// be where name came from, and reports what each match proves.
//
// Adoption takes a copy off this machine and records no provenance, so a skill
// that arrived that way has nothing to update from. Recovering that link is
// guesswork in general; it stops being guesswork when the content is identical,
// and this reports the difference rather than flattening it. Nothing is
// written; the caller decides, because attributing a skill to the wrong author
// is a worse failure than leaving it unattributed.
func (s *Store) FindUpstream(ctx context.Context, name string) ([]Candidate, error) {
	if e := ValidateName(name); e != nil {
		return nil, e
	}
	unlock, e := s.lock()
	if e != nil {
		return nil, e
	}
	d, e := s.load()
	unlock()
	if e != nil {
		return nil, e
	}
	entry, ok := d.Entries[name]
	if !ok {
		return nil, errors.New("not installed")
	}
	ours, e := digestContext(ctx, s.entryPath(entry))
	if e != nil {
		return nil, e
	}
	mine, e := ReadSkill(s.entryPath(entry))
	if e != nil {
		return nil, e
	}
	sources, e := s.Sources()
	if e != nil {
		return nil, e
	}
	var out []Candidate
	for _, source := range sources {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		c, err := OpenCollectionContext(ctx, source)
		if err != nil {
			// A source that cannot be opened right now is not a reason to
			// abandon the others; it simply holds no candidates today.
			continue
		}
		for _, sk := range c.Skills {
			if sk.Name != name {
				continue
			}
			rel, relErr := filepath.Rel(c.Root, sk.Path)
			if relErr != nil {
				continue
			}
			out = append(out, Candidate{
				Upstream:        source,
				UpstreamPath:    filepath.ToSlash(rel),
				Name:            sk.Name,
				Description:     sk.Description,
				Identical:       s.sameContent(ctx, sk.Path, ours),
				SameDescription: sk.Description == mine.Description,
			})
		}
		c.Close()
	}
	slices.SortStableFunc(out, func(a, b Candidate) int { return a.Strength() - b.Strength() })
	return out, nil
}

// sameContent reports whether a source tree would publish to exactly the bytes
// already in the library, normalising it the way an import does first.
func (s *Store) sameContent(ctx context.Context, path, want string) bool {
	stage, e := os.MkdirTemp(s.Root, ".import-")
	if e != nil {
		return false
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if e = copySkill(path, filepath.Join(stage, "content")); e != nil {
		return false
	}
	got, e := digestContext(ctx, filepath.Join(stage, "content"))
	return e == nil && got == want
}

// SetUpstream records where a skill is published, which is what makes checking
// and updating possible for a skill that was adopted rather than imported.
func (s *Store) SetUpstream(name, upstream, upstreamPath string) error {
	if e := ValidateName(name); e != nil {
		return e
	}
	if upstream == "" {
		return errors.New("an upstream is required")
	}
	unlock, e := s.lock()
	if e != nil {
		return e
	}
	defer unlock()
	d, e := s.load()
	if e != nil {
		return e
	}
	entry, ok := d.Entries[name]
	if !ok {
		return errors.New("not installed")
	}
	entry.Upstream, entry.UpstreamPath = upstream, upstreamPath
	d.Entries[name] = entry
	return s.save(d)
}

// Compare reports whether the library copy of a skill still matches its source.
// It is the question Refresh answers destructively, asked without changing
// anything.
//
// The source is normalised through the same copy an import performs before it
// is hashed. That step is the whole trick: copySkill settles directory
// permissions and resolves symlinks, so hashing a source tree directly would
// report drift for two identical skills that merely came off disk differently.
//
// A difference means only that the two sides disagree. It does not say which
// one moved, because nothing records what the copy looked like when it arrived.
func (s *Store) Compare(ctx context.Context, name string) (Drift, error) {
	var out Drift
	if e := ValidateName(name); e != nil {
		return out, e
	}
	unlock, e := s.lock()
	if e != nil {
		return out, e
	}
	d, e := s.load()
	// The fetch below reaches the network, which is far too long to hold the
	// library lock for when nothing is being written.
	unlock()
	if e != nil {
		return out, e
	}
	entry, ok := d.Entries[name]
	if !ok {
		return out, errors.New("not installed")
	}
	source, _ := sourceRef(entry)
	if filepath.IsAbs(Expand(source)) {
		if _, err := os.Stat(Expand(source)); errors.Is(err, os.ErrNotExist) {
			// Adopted skills land here: their source is the custody directory
			// the original was kept in, which the user may since have cleaned
			// up. There is nothing to compare against until a source is set.
			return out, fmt.Errorf("source no longer exists: %s; use skillverk add SOURCE --replace to point this skill at one", source)
		}
	}
	c, e := OpenCollectionContext(ctx, source)
	if e != nil {
		return out, e
	}
	defer c.Close()
	resolved, e := sourceSkillPath(c, entry, name)
	if e != nil {
		return out, e
	}
	stage, e := os.MkdirTemp(s.Root, ".import-")
	if e != nil {
		return out, e
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if e = copySkill(resolved, filepath.Join(stage, "content")); e != nil {
		return out, e
	}
	theirs, e := digestContext(ctx, filepath.Join(stage, "content"))
	if e != nil {
		return out, e
	}
	ours, e := digestContext(ctx, s.entryPath(entry))
	if e != nil {
		return out, e
	}
	out.Same = ours == theirs
	if entry.Digest == "" {
		return out, nil
	}
	out.Baseline = true
	out.Edited = ours != entry.Digest
	out.Moved = theirs != entry.Digest
	return out, nil
}

// Drift is the verdict of a comparison between a library copy and its source.
//
// Same answers the only question that never needs a baseline. The rest need one:
// knowing the content as it arrived is what separates a copy the source has
// moved past from a copy that was edited here, and those deserve opposite
// advice. Entries imported before the baseline was recorded report Baseline
// false, and the caller should say no more than "these differ".
type Drift struct {
	Same     bool
	Baseline bool
	Edited   bool
	Moved    bool
}

// Verdict names the state in the words a person would use.
func (d Drift) Verdict() string {
	switch {
	case d.Same:
		return "up to date"
	case !d.Baseline:
		return "different"
	case d.Moved && d.Edited:
		return "diverged"
	case d.Moved:
		return "out of date"
	case d.Edited:
		return "edited here"
	}
	// Both sides match the baseline yet differ from each other, which cannot
	// happen; report it as the plain fact rather than guessing.
	return "different"
}

func (s *Store) Refresh(names []string) ([]Result, error) {
	unlock, e := s.lock()
	if e != nil {
		return nil, e
	}
	defer unlock()
	d, e := s.load()
	if e != nil {
		return nil, e
	}
	if len(names) == 0 {
		for n := range d.Entries {
			names = append(names, n)
		}
		slices.Sort(names)
	}
	var rs []Result
	for _, n := range names {
		r := Result{Name: n, Action: "refresh"}
		entry, ok := d.Entries[n]
		if !ok {
			r.Error = "not installed"
			rs = append(rs, r)
			continue
		}
		source, _ := sourceRef(entry)
		if filepath.IsAbs(Expand(source)) {
			if _, err := os.Stat(Expand(source)); errors.Is(err, os.ErrNotExist) {
				r.Error = fmt.Sprintf("refresh source no longer exists: %s; shared content is preserved. Use skillverk add SOURCE --replace to set an available source", source)
				rs = append(rs, r)
				continue
			}
		}
		c, err := OpenCollection(source)
		if err == nil {
			resolved, er := sourceSkillPath(c, entry, n)
			if er != nil {
				err = er
			} else {
				sk, er := ReadSkill(resolved)
				err = er
				if err == nil && sk.Name != n {
					err = errors.New("source skill name changed")
				}
				if err == nil {
					chosen, checkErr := s.dependencySelection(c, []Skill{sk}, d)
					if checkErr != nil {
						err = checkErr
					} else if len(chosen) != 1 {
						err = errors.New("update needs additional sibling skills; import them from the collection first")
					} else {
						err = s.publishOne(&d, sk, entry)
					}
				}
			}
			c.Close()
		}
		if err != nil {
			r.Error = err.Error()
			fresh, loadErr := s.load()
			if loadErr != nil {
				return append(rs, r), errors.Join(err, loadErr)
			}
			d = fresh
		}
		rs = append(rs, r)
	}
	return rs, ResultsError(rs)
}
func (s *Store) Path(name string) (string, error) {
	if e := ValidateName(name); e != nil {
		return "", e
	}
	u, e := s.lock()
	if e != nil {
		return "", e
	}
	defer u()
	d, e := s.load()
	if e != nil {
		return "", e
	}
	x, ok := d.Entries[name]
	if !ok {
		return "", fmt.Errorf("unknown skill %q", name)
	}
	return s.entryPath(x), nil
}
func key(name, agent string) string { return name + "/" + agent }
func linkTarget(path string) string {
	target, e := os.Readlink(path)
	if e != nil {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.Clean(target)
}
func samePath(a, b string) bool {
	if filepath.Separator == '\\' {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

type contextReader struct {
	context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.Err(); e != nil {
		return 0, e
	}
	return r.Reader.Read(p)
}
