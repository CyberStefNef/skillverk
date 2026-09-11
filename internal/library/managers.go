package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type ManagerRecord struct {
	Single    bool   `json:"single,omitempty"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Manager   string `json:"manager"`
	Digest    string `json:"digest"`
	Tracked   bool   `json:"tracked,omitempty"`
	Pinned    bool   `json:"pinned,omitempty"`
	Source    string `json:"source,omitempty"`
	SkillPath string `json:"skill_path,omitempty"`
}

func bytesDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func managerDocument(path string) ([]byte, map[string]json.RawMessage, map[string]json.RawMessage, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return nil, nil, nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, nil, nil, fmt.Errorf("installer record is not a regular file: %s", path)
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, nil, nil, e
	}
	var doc map[string]json.RawMessage
	if e = json.Unmarshal(b, &doc); e != nil {
		return nil, nil, nil, e
	}
	var version int
	if e = json.Unmarshal(doc["version"], &version); e != nil || version < 1 || version > 3 {
		return nil, nil, nil, fmt.Errorf("unsupported installer record version: %s", path)
	}
	var skills map[string]json.RawMessage
	if e = json.Unmarshal(doc[managerMapKey(path)], &skills); e != nil || skills == nil {
		return nil, nil, nil, fmt.Errorf("invalid installer skills record: %s", path)
	}
	return b, doc, skills, nil
}
func managerRecords(repo, skillPath, name string) ([]ManagerRecord, error) {
	if info, err := os.Stat(skillPath); err == nil && info.Mode().IsRegular() {
		temp, err := TempDir("flat-layout-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(temp)
		if err = copySkill(skillPath, temp); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(temp)
		if err != nil {
			return nil, err
		}
		if len(entries) > 1 {
			return nil, errors.New("flat Markdown uses adjacent resources; keep this native installation so its resource paths and updates stay consistent; importing a shared copy is supported")
		}
	}

	if repo == "" {
		if project, err := Project(filepath.Dir(skillPath)); err == nil && project != "" {
			rel, _ := filepath.Rel(project, skillPath)
			tracked, err := gitAt(project, "ls-files", "--", filepath.ToSlash(rel))
			if err != nil {
				return nil, err
			}
			if tracked != "" {
				return nil, fmt.Errorf("Git-tracked installation; review it in its owning repository %s", project)
			}
		}
	}
	if resolved, e := resolvePath(skillPath); e == nil {
		if owner := installedBundleOwner(resolved); owner != "" {
			return nil, fmt.Errorf("%s owns this installation; keep its skill with the package", owner)
		}
	}
	home, _ := os.UserHomeDir()
	candidates := map[string]string{}
	if repo != "" {
		candidates[filepath.Join(repo, "skills-lock.json")] = "skills CLI"
	} else {
		base := filepath.Join(home, ".agents")
		if state := os.Getenv("XDG_STATE_HOME"); state != "" {
			base = filepath.Join(state, "skills")
		}
		candidates[filepath.Join(base, ".skill-lock.json")] = "skills CLI"
	}
	for parent := filepath.Dir(skillPath); ; parent = filepath.Dir(parent) {
		candidates[filepath.Join(parent, ".hub", "lock.json")] = "Hermes Hub"
		if raw, err := os.ReadFile(filepath.Join(parent, ".bundled_manifest")); err == nil {
			rel, _ := filepath.Rel(parent, skillPath)
			for _, line := range strings.Split(string(raw), "\n") {
				key := strings.SplitN(strings.TrimSpace(line), ":", 2)[0]
				if key == name || key == filepath.ToSlash(rel) {
					return nil, fmt.Errorf("Hermes bundled skill; keep %s with Hermes", name)
				}
			}
		}
		if Exists(filepath.Join(parent, "tile.json")) {
			return nil, fmt.Errorf("Tessl tile-owned skill; keep %s with its tile", name)
		}
		for _, dir := range []string{".clawhub", ".clawdhub"} {
			candidates[filepath.Join(parent, dir, "lock.json")] = "ClawHub"
		}
		if parent == repo || parent == home || filepath.Dir(parent) == parent {
			break
		}
	}
	var paths []string
	for path := range candidates {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	records, err := singleManagerRecords(repo, skillPath, name)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		if !Exists(path) {
			continue
		}
		b, _, skills, e := managerDocument(path)
		if e != nil {
			return nil, e
		}
		raw, ok := skills[name]
		if !ok {
			continue
		}
		var entry struct {
			Pinned      bool   `json:"pinned"`
			PluginName  string `json:"pluginName"`
			Source      string `json:"source"`
			SourceURL   string `json:"sourceUrl"`
			SourceType  string `json:"sourceType"`
			Ref         string `json:"ref"`
			SkillPath   string `json:"skillPath"`
			Owner       string `json:"ownerHandle"`
			Version     string `json:"version"`
			Identifier  string `json:"identifier"`
			InstallPath string `json:"install_path"`
		}
		if e = json.Unmarshal(raw, &entry); e != nil {
			return nil, e
		}
		if entry.PluginName != "" {
			return nil, fmt.Errorf("%s is owned by plugin %s; keep it with its plugin manager", name, entry.PluginName)
		}
		if candidates[path] == "Hermes Hub" && entry.InstallPath != "" {
			target := filepath.Join(filepath.Dir(filepath.Dir(path)), filepath.FromSlash(entry.InstallPath))
			if !samePath(target, skillPath) {
				continue
			}
		}
		tracked := false
		if repo != "" && within(path, repo) {
			rel, _ := filepath.Rel(repo, path)
			out, e := gitAt(repo, "ls-files", "--", filepath.ToSlash(rel))
			if e != nil {
				return nil, e
			}
			tracked = out != ""
		}
		upstream := entry.SourceURL
		subpath := strings.TrimSuffix(entry.SkillPath, "/SKILL.md")
		if upstream == "" && entry.SourceType == "github" {
			upstream = "https://github.com/" + entry.Source + ".git"
		}
		if entry.SourceType == "github" && entry.Ref != "" {
			upstream = "https://github.com/" + entry.Source + ".git?ref=" + url.QueryEscape(entry.Ref)
		}
		if candidates[path] == "Hermes Hub" {
			if strings.HasPrefix(entry.Identifier, "https://") {
				upstream = entry.Identifier
			} else if entry.Source == "github" {
				parts := strings.SplitN(entry.Identifier, "/", 3)
				if len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != "" && !strings.Contains(entry.Identifier, "..") && !strings.ContainsAny(entry.Identifier, "\\?#@:") {
					upstream = "https://github.com/" + parts[0] + "/" + parts[1] + ".git"
					subpath = parts[2]
				}
			}
			if upstream == "" {
				return nil, errors.New("Hermes Hub source cannot yet be refreshed by Skillverk; keep this installation with Hermes")
			}
		}
		if candidates[path] == "ClawHub" {
			registry := "https://clawhub.ai"
			slug := name
			for _, dir := range []string{".clawhub", ".clawdhub"} {
				originPath := filepath.Join(skillPath, dir, "origin.json")
				data, err := os.ReadFile(originPath)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return nil, err
				}
				var origin struct {
					Version  int    `json:"version"`
					Registry string `json:"registry"`
					Slug     string `json:"slug"`
					Owner    string `json:"ownerHandle"`
				}
				if err := json.Unmarshal(data, &origin); err != nil || origin.Version != 1 || origin.Registry == "" || origin.Slug == "" {
					return nil, fmt.Errorf("invalid ClawHub origin record: %s", originPath)
				}
				registry, slug = strings.TrimRight(origin.Registry, "/"), origin.Slug
				if entry.Owner == "" {
					entry.Owner = origin.Owner
				}
				break
			}
			registryURL, err := url.Parse(registry)
			if err != nil {
				return nil, err
			}
			if err := checkSourceURL(registryURL); err != nil {
				return nil, err
			}
			upstream = "https://clawhub.ai/" + slug
			if entry.Owner != "" {
				upstream = "https://clawhub.ai/" + entry.Owner + "/skills/" + slug
			}
			if registry != "https://clawhub.ai" {
				registryURL.Path = strings.TrimRight(registryURL.Path, "/") + "/api/v1/download"
				query := url.Values{"slug": []string{slug}}
				if entry.Owner != "" {
					query.Set("ownerHandle", entry.Owner)
				}
				registryURL.RawQuery = query.Encode()
				upstream = registryURL.String()
			}
			if entry.Pinned && entry.Version != "" {
				sourceURL, err := url.Parse(upstream)
				if err != nil {
					return nil, err
				}
				query := sourceURL.Query()
				query.Set("version", entry.Version)
				sourceURL.RawQuery = query.Encode()
				upstream = sourceURL.String()
			}
			subpath = ""
		}
		records = append(records, ManagerRecord{Source: upstream, SkillPath: subpath, Path: path, Name: name, Manager: candidates[path], Digest: bytesDigest(b), Tracked: tracked, Pinned: entry.Pinned})
	}
	return records, nil
}

type managerEdit struct {
	Path   string
	Before []byte
	After  []byte
	Mode   os.FileMode
}
type managerTarget struct {
	Path    string
	Name    string
	Removed bool
}
type managerTransfer struct {
	Edits   []managerEdit
	Targets []managerTarget
}

func (s *Store) beginManagerTransfer(items []SetupItem) (string, error) {
	var tx managerTransfer
	byPath := map[string][]ManagerRecord{}
	for _, item := range items {
		if item.Action != "share" && item.Action != "replace-shared" && item.Action != "remove-broken" {
			continue
		}
		for _, r := range item.Managers {
			byPath[r.Path] = append(byPath[r.Path], r)
		}
		if len(item.Managers) > 0 {
			tx.Targets = append(tx.Targets, managerTarget{Path: item.Path, Name: item.Name, Removed: item.Action == "remove-broken"})
		}
	}
	if len(byPath) == 0 {
		return "", nil
	}
	var paths []string
	for p := range byPath {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		if byPath[p][0].Single {
			b, err := os.ReadFile(p)
			if err != nil {
				return "", err
			}
			for _, r := range byPath[p] {
				if bytesDigest(b) != r.Digest {
					return "", fmt.Errorf("installer record changed; rescan: %s", p)
				}
			}
			info, err := os.Stat(p)
			if err != nil {
				return "", err
			}
			tx.Edits = append(tx.Edits, managerEdit{p, b, []byte("{\"managedBy\":\"skillverk\"}\n"), info.Mode().Perm()})
			continue
		}
		b, doc, skills, e := managerDocument(p)
		if e != nil {
			return "", e
		}
		for _, r := range byPath[p] {
			if bytesDigest(b) != r.Digest {
				return "", fmt.Errorf("installer record changed; rescan: %s", p)
			}
			delete(skills, r.Name)
		}
		doc[managerMapKey(p)], _ = json.Marshal(skills)
		after, e := json.MarshalIndent(doc, "", "  ")
		if e != nil {
			return "", e
		}
		info, e := os.Stat(p)
		if e != nil {
			return "", e
		}
		tx.Edits = append(tx.Edits, managerEdit{p, b, append(after, '\n'), info.Mode().Perm()})
	}
	journal := filepath.Join(s.Root, "manager-transfer.json")
	if Exists(journal) {
		return "", errors.New("an installer handoff needs recovery; run setup again")
	}
	if e := writeJSON(journal, tx); e != nil {
		return "", e
	}
	for _, edit := range tx.Edits {
		current, e := os.ReadFile(edit.Path)
		if e != nil {
			return journal, e
		}
		if bytesDigest(current) != bytesDigest(edit.Before) {
			return journal, fmt.Errorf("installer record changed during handoff: %s", edit.Path)
		}
		if e := atomicWrite(edit.Path, edit.After, edit.Mode); e != nil {
			return journal, e
		}
	}
	return journal, nil
}

// The setup lock serializes this journal across scans and applications. Recovery
// never overwrites a record another tool edited after the handoff began.
func (s *Store) finishManagerTransfer(success bool) error {
	journal := filepath.Join(s.Root, "manager-transfer.json")
	var tx managerTransfer
	if e := readJSON(journal, &tx); errors.Is(e, os.ErrNotExist) {
		return nil
	} else if e != nil {
		return e
	}
	anyLinked, allLinked := false, true
	var db database
	if e := readJSON(filepath.Join(s.Root, "library.json"), &db); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	for _, t := range tx.Targets {
		linked := false
		if t.Removed {
			linked = !Exists(t.Path)
		} else if entry, ok := db.Entries[t.Name]; ok {
			linked = samePath(linkTarget(t.Path), s.entryPath(entry)) || samePath(linkTarget(t.Path), filepath.Join(s.entryPath(entry), "SKILL.md"))
		}
		anyLinked = anyLinked || linked
		allLinked = allLinked && linked
	}
	if success || allLinked {
		return os.Remove(journal)
	}
	if anyLinked {
		return errors.New("partial installer handoff retained; repair the remaining skill links before running setup again")
	}
	for _, edit := range tx.Edits {
		actual, e := os.ReadFile(edit.Path)
		if e != nil {
			return e
		}
		if bytesDigest(actual) == bytesDigest(edit.Before) {
			continue
		}
		if bytesDigest(actual) != bytesDigest(edit.After) {
			return fmt.Errorf("installer record changed externally; recovery preserved it: %s", edit.Path)
		}
		if e = atomicWrite(edit.Path, edit.Before, edit.Mode); e != nil {
			return e
		}
	}
	return os.Remove(journal)
}
func (s *Store) setupLock(ctx context.Context) (func(), error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(s.Root, 0700); e != nil {
		return nil, e
	}
	type acquired struct {
		unlock func()
		err    error
	}
	ready := make(chan acquired)
	go func() {
		u, e := fileLock(filepath.Join(s.Root, "setup.lock"))
		select {
		case ready <- acquired{u, e}:
		case <-ctx.Done():
			if u != nil {
				u()
			}
		}
	}()
	select {
	case result := <-ready:
		if e := ctx.Err(); e != nil {
			if result.unlock != nil {
				result.unlock()
			}
			return nil, e
		}
		return result.unlock, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}

}
func (i SetupItem) ManagerPreview() string {
	var b strings.Builder
	if i.Owner == "hermes" && (i.Action == "share" || i.Action == "replace-shared") {
		b.WriteString("Hermes can edit shared skills in place. Those edits affect every agent using this library copy.\n")
	}
	for _, r := range i.Managers {
		fmt.Fprintf(&b, "Stop %s updates for %s by removing its entry from %s.\n", r.Manager, r.Name, r.Path)
		if r.Pinned {
			b.WriteString("The current local content is imported; its pinned registry version remains the update source.\n")
		}
		if r.Tracked {
			b.WriteString("The installer record stays Git-tracked; its edit is left unstaged.\n")
		}
	}
	return b.String()
}

func (s *Store) recordManagerSource(item SetupItem) error {
	for _, record := range item.Managers {
		if record.Source == "" {
			continue
		}
		u, e := s.lock()
		if e != nil {
			return e
		}
		defer u()
		d, e := s.load()
		if e != nil {
			return e
		}
		entry, ok := d.Entries[item.Name]
		if !ok {
			return errors.New("handoff import is missing")
		}
		entry.Upstream = record.Source
		entry.UpstreamPath = record.SkillPath
		d.Entries[item.Name] = entry
		return s.save(d)
	}
	return nil
}

func managerMapKey(path string) string {
	if filepath.Base(filepath.Dir(path)) == ".hub" {
		return "installed"
	}
	return "skills"
}
func singleManagerRecords(repo, path, name string) ([]ManagerRecord, error) {
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return nil, nil
	}
	file := filepath.Join(path, ".openskills.json")
	info, err := os.Lstat(file)
	if os.IsNotExist(err) || (err != nil && !Exists(path)) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("installer metadata is not a regular file: %s", file)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var m struct {
		ManagedBy  string `json:"managedBy"`
		SourceType string `json:"sourceType"`
		Repo       string `json:"repoUrl"`
		Local      string `json:"localPath"`
		Subpath    string `json:"subpath"`
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.ManagedBy == "skillverk" {
		return nil, nil
	}
	source := m.Repo
	if m.SourceType == "local" {
		source = m.Local
	}
	if source == "" {
		return nil, fmt.Errorf("OpenSkills metadata has no source: %s", file)
	}
	tracked := false
	if repo != "" {
		rel, _ := filepath.Rel(repo, file)
		out, e := gitAt(repo, "ls-files", "--", filepath.ToSlash(rel))
		if e != nil {
			return nil, e
		}
		tracked = out != ""
	}
	return []ManagerRecord{{Path: file, Name: name, Manager: "OpenSkills", Digest: bytesDigest(raw), Source: source, SkillPath: m.Subpath, Tracked: tracked, Single: true}}, nil
}
