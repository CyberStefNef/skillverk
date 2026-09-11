package library

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const downloadLimit = 64 << 20
const archiveFileLimit = 4096

var sourceHTTP = &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return errors.New("skill source redirected away from HTTPS")
	}
	if len(via) >= 5 {
		return errors.New("too many source redirects")
	}
	return checkSourceURL(req.URL)
}}

func checkSourceURL(u *url.URL) error {
	if u.User != nil {
		return errors.New("source URLs must not contain credentials")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return errors.New("skill downloads require HTTPS")
	}
	return nil
}
func fetchSkillBytes(ctx context.Context, value string) ([]byte, error) {
	u, e := url.Parse(value)
	if e != nil {
		return nil, e
	}
	if e = checkSourceURL(u); e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", value, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "Skillverk")
	var res *http.Response
	for attempt := 0; ; attempt++ {
		res, e = sourceHTTP.Do(req)
		if e != nil {
			return nil, e
		}
		if attempt >= 2 || (res.StatusCode != http.StatusTooManyRequests && res.StatusCode != http.StatusServiceUnavailable) {
			break
		}
		delay := time.Duration(attempt+1) * time.Second
		retryAfter := res.Header.Get("Retry-After")
		if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
			if seconds > 30 {
				break
			}
			delay = time.Duration(seconds) * time.Second
		} else if until, err := http.ParseTime(retryAfter); err == nil {
			delay = time.Until(until)
			if delay > 30*time.Second {
				break
			}
			if delay < 0 {
				delay = 0
			}
		}
		res.Body.Close()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("source returned HTTP %d: %s", res.StatusCode, u.Redacted())
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, downloadLimit+1))
	if e != nil {
		return nil, e
	}
	if len(b) > downloadLimit {
		return nil, errors.New("skill download exceeds 64 MiB")
	}
	return b, nil
}
func resourcePath(root, name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") || strings.Contains(name, ":") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("invalid resource path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == ".git" || part == ".skillverk-selection.json" {
			return "", fmt.Errorf("unsafe resource path %q", name)
		}
	}
	p := filepath.Join(root, filepath.FromSlash(name))
	if !within(p, root) {
		return "", errors.New("resource escapes skill")
	}
	return p, nil
}
func unpackSkills(ctx context.Context, data []byte, root string) error {
	total, count := int64(0), 0
	seen := map[string]bool{}
	write := func(name string, mode os.FileMode, reader io.Reader) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		p, e := resourcePath(root, name)
		if e != nil {
			return e
		}
		key := strings.ToLower(filepath.Clean(p))
		if seen[key] {
			return fmt.Errorf("duplicate archive path %s", name)
		}
		seen[key] = true
		count++
		if count > archiveFileLimit {
			return errors.New("too many archive entries")
		}
		if mode.IsDir() {
			return os.MkdirAll(p, 0755)
		}
		if !mode.IsRegular() {
			return fmt.Errorf("archive links and special files are unsupported: %s", name)
		}
		if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			return e
		}
		permissions := os.FileMode(0644)
		if mode.Perm()&0111 != 0 {
			permissions = 0755
		}
		f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, permissions)
		if e != nil {
			return e
		}
		n, e := io.Copy(f, io.LimitReader(contextReader{ctx, reader}, downloadLimit-total+1))
		total += n
		e = errors.Join(e, f.Close())
		if total > downloadLimit {
			return errors.New("expanded archive exceeds 64 MiB")
		}
		return e
	}
	if bytes.HasPrefix(data, []byte("PK")) {
		z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if e != nil {
			return e
		}
		for _, f := range z.File {
			r, e := f.Open()
			if e != nil {
				return e
			}
			e = write(f.Name, f.Mode(), r)
			e = errors.Join(e, r.Close())
			if e != nil {
				return e
			}
		}
		return nil
	}
	var reader io.Reader = bytes.NewReader(data)
	if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
		gz, e := gzip.NewReader(reader)
		if e != nil {
			return e
		}
		defer gz.Close()
		reader = gz
	}
	tr := tar.NewReader(reader)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			if count == 0 {
				return errors.New("empty skill archive")
			}
			return nil
		}
		if e != nil {
			return e
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			return fmt.Errorf("archive links and special files are unsupported: %s", h.Name)
		}
		mode := os.FileMode(h.Mode)
		if h.Typeflag == tar.TypeDir {
			mode |= os.ModeDir
		}
		if e = write(h.Name, mode, tr); e != nil {
			return e
		}
	}
}
func archiveSource(ctx context.Context, value string, local bool) (string, func(), error) {
	var b []byte
	var e error
	if local {
		f, err := os.Open(value)
		if err != nil {
			return "", nil, err
		}
		b, e = io.ReadAll(io.LimitReader(contextReader{ctx, f}, downloadLimit+1))
		e = errors.Join(e, f.Close())
		if len(b) > downloadLimit {
			e = errors.New("skill archive exceeds 64 MiB")
		}
	} else {
		b, e = fetchSkillBytes(ctx, value)
	}
	if e != nil {
		return "", nil, e
	}
	tmp, e := TempDir("archive-*")
	if e != nil {
		return "", nil, e
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	var handoff struct {
		SourceRef  string `json:"sourceRef"`
		ArchiveURL string `json:"archiveUrl"`
		Path       string `json:"path"`
	}
	if !local && json.Unmarshal(b, &handoff) == nil && handoff.SourceRef == "public-github" {
		if handoff.ArchiveURL == "" {
			cleanup()
			return "", nil, errors.New("registry handoff has no archive")
		}
		b, e = fetchReferencedSkillBytes(ctx, value, handoff.ArchiveURL)
		if e != nil {
			cleanup()
			return "", nil, e
		}
	}
	if e = unpackSkills(ctx, b, tmp); e != nil {
		cleanup()
		return "", nil, e
	}
	if handoff.SourceRef == "public-github" {
		entries, e := os.ReadDir(tmp)
		if e != nil {
			cleanup()
			return "", nil, e
		}
		base := tmp
		if len(entries) == 1 && entries[0].IsDir() {
			base = filepath.Join(tmp, entries[0].Name())
		}
		selected, e := resourcePath(base, handoff.Path)
		if e != nil {
			cleanup()
			return "", nil, e
		}
		skill, e := ReadSkill(selected)
		if e != nil {
			cleanup()
			return "", nil, e
		}
		if e = writeJSON(filepath.Join(base, ".skillverk-selection.json"), []string{skill.Name}); e != nil {
			cleanup()
			return "", nil, e
		}
		return base, cleanup, nil
	}
	return unwrapArchive(tmp), cleanup, nil
}
func unwrapArchive(root string) string {
	for !Exists(filepath.Join(root, "SKILL.md")) {
		entries, e := os.ReadDir(root)
		if e != nil || len(entries) != 1 || !entries[0].IsDir() {
			break
		}
		root = filepath.Join(root, entries[0].Name())
	}
	return root
}
func archiveURL(p string) bool {
	p = strings.ToLower(p)
	return strings.HasSuffix(p, ".skill") || strings.HasSuffix(p, ".zip") || strings.HasSuffix(p, ".tar.gz") || strings.HasSuffix(p, ".tgz") || strings.HasSuffix(p, ".tar")
}
func isServiceURL(value string) bool {
	u, e := url.Parse(value)
	if e != nil {
		return false
	}
	return u.Scheme == "http" || (u.Scheme == "https" && u.Hostname() != "github.com" && u.Hostname() != "gitlab.com" && !strings.HasSuffix(u.Path, ".git")) || archiveURL(u.Path)
}

type skillIndex struct {
	Skills []struct {
		Name   string   `json:"name"`
		Files  []string `json:"files"`
		Type   string   `json:"type"`
		URL    string   `json:"url"`
		Digest string   `json:"digest"`
	} `json:"skills"`
}

func wellKnownSource(ctx context.Context, value string) (string, func(), error) {
	u, e := url.Parse(value)
	if e != nil {
		return "", nil, e
	}
	if e = checkSourceURL(u); e != nil {
		return "", nil, e
	}
	// Never replace a scoped collection with the host's entire catalog.
	var candidates []string
	if strings.HasSuffix(u.Path, "index.json") {
		candidates = []string{value}
	} else {
		for _, dir := range []string{"agent-skills", "skills"} {
			v := *u
			v.Path = strings.TrimRight(u.Path, "/") + "/.well-known/" + dir + "/index.json"
			v.RawQuery = ""
			candidates = append(candidates, v.String())
		}
		v := *u
		v.Path = strings.TrimRight(u.Path, "/") + "/index.json"
		v.RawQuery = ""
		candidates = append(candidates, v.String())
	}
	var raw []byte
	var indexURL string
	for _, candidate := range candidates {
		raw, e = fetchSkillBytes(ctx, candidate)
		if e == nil {
			indexURL = candidate
			break
		}
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
	}
	if indexURL == "" {
		return "", nil, fmt.Errorf("no skill index at this URL; use a Git source, archive, or published skill collection: %w", e)
	}
	var index skillIndex
	if e = json.Unmarshal(raw, &index); e != nil {
		return "", nil, e
	}
	if len(index.Skills) == 0 || len(index.Skills) > archiveFileLimit {
		return "", nil, errors.New("invalid or empty skill index")
	}
	tmp, e := TempDir("service-*")
	if e != nil {
		return "", nil, e
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	fail := func(e error) (string, func(), error) { cleanup(); return "", nil, e }
	base, _ := url.Parse(indexURL)
	names := map[string]bool{}
	total := 0
	count := 0
	for _, sk := range index.Skills {
		if e = ValidateName(sk.Name); e != nil {
			return fail(e)
		}
		if names[sk.Name] {
			return fail(fmt.Errorf("duplicate skill name %s", sk.Name))
		}
		names[sk.Name] = true
		root := filepath.Join(tmp, sk.Name)
		if e = os.MkdirAll(root, 0755); e != nil {
			return fail(e)
		}
		if sk.URL != "" {
			target, e := base.Parse(sk.URL)
			if e != nil {
				return fail(e)
			}
			b, e := fetchReferencedSkillBytes(ctx, indexURL, target.String())
			if e != nil {
				return fail(e)
			}
			total += len(b)
			sum := sha256.Sum256(b)
			want := strings.TrimPrefix(sk.Digest, "sha256:")
			if len(want) != 64 || !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
				return fail(fmt.Errorf("artifact digest mismatch for %s", sk.Name))
			}
			switch sk.Type {
			case "skill-md":
				e = os.WriteFile(filepath.Join(root, "SKILL.md"), b, 0644)
			case "archive":
				e = unpackSkills(ctx, b, root)
				if e == nil {
					e = flattenSkillArchive(root)
				}
			default:
				e = fmt.Errorf("unsupported skill artifact type %q", sk.Type)
			}
			if e != nil {
				return fail(e)
			}
		} else {
			if len(sk.Files) == 0 {
				return fail(errors.New("skill index has no files"))
			}
			seen := map[string]bool{}
			for _, name := range sk.Files {
				p, e := resourcePath(root, name)
				if e != nil {
					return fail(e)
				}
				key := strings.ToLower(p)
				if seen[key] {
					return fail(fmt.Errorf("duplicate skill file %s", name))
				}
				seen[key] = true
				relative := url.URL{Path: sk.Name + "/" + name}
				target := base.ResolveReference(&relative)
				b, e := fetchReferencedSkillBytes(ctx, indexURL, target.String())
				if e != nil {
					return fail(e)
				}
				total += len(b)
				count++
				if total > downloadLimit || count > archiveFileLimit {
					return fail(errors.New("skill collection exceeds download limits"))
				}
				if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
					return fail(e)
				}
				if e = os.WriteFile(p, b, 0644); e != nil {
					return fail(e)
				}
			}
		}
		if total > downloadLimit {
			return fail(errors.New("skill collection exceeds download limits"))
		}
		if e = checkDownloadTree(tmp); e != nil {
			return fail(e)
		}
		if !Exists(filepath.Join(root, "SKILL.md")) && Exists(filepath.Join(root, sk.Name+".md")) {
			raw, err := os.ReadFile(filepath.Join(root, sk.Name+".md"))
			if err != nil {
				return fail(err)
			}
			raw, err = normalizeFlatEntry(raw, sk.Name)
			if err != nil {
				return fail(err)
			}
			if err = os.WriteFile(filepath.Join(root, "SKILL.md"), raw, 0644); err != nil {
				return fail(err)
			}
		}
		parsed, e := ReadSkill(root)
		if e != nil {
			return fail(e)
		}
		if parsed.Name != sk.Name {
			return fail(errors.New("skill index and content names differ"))
		}
	}
	return tmp, cleanup, nil
}

var githubSkillLink = regexp.MustCompile(`https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/(?:blob|tree)/[^\s"<>]+`)

func serviceSource(ctx context.Context, value string) (string, func(), error) {
	u, e := url.Parse(value)
	if e != nil {
		return "", nil, e
	}
	host := strings.TrimPrefix(u.Hostname(), "www.")
	if archiveURL(u.Path) || strings.HasSuffix(u.Path, "/api/v1/download") {
		return archiveSource(ctx, value, false)
	}
	if host == "clawhub.ai" || host == "clawhub.com" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		var slug, owner string
		switch {
		case len(parts) == 3 && parts[1] == "skills":
			slug = parts[2]
			owner = strings.TrimPrefix(parts[0], "@")
		case len(parts) == 2:
			slug = parts[1]
			owner = strings.TrimPrefix(parts[0], "@")
		case len(parts) == 1:
			slug = parts[0]
		default:
			return "", nil, errors.New("use a ClawHub skill listing URL")
		}
		api := *u
		api.Path = "/api/v1/download"
		q := url.Values{"slug": []string{slug}}
		if owner != "" {
			q.Set("ownerHandle", owner)
		}
		for _, k := range []string{"version", "tag"} {
			if v := u.Query().Get(k); v != "" {
				q.Set(k, v)
			}
		}
		api.RawQuery = q.Encode()
		return archiveSource(ctx, api.String(), false)
	}
	if host == "skillsmp.com" || host == "playbooks.com" || host == "context7.com" {
		body, e := fetchSkillBytes(ctx, value)
		if e != nil {
			return "", nil, e
		}
		links := map[string]bool{}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		sourcePrefix := ""
		if host == "playbooks.com" && len(parts) >= 4 && parts[0] == "skills" {
			sourcePrefix = "https://github.com/" + parts[1] + "/" + parts[2] + "/"
		}
		for _, m := range githubSkillLink.FindAllString(html.UnescapeString(string(body)), -1) {
			m = strings.TrimRight(m, "\\")
			if sourcePrefix != "" && !strings.HasPrefix(m, sourcePrefix) {
				continue
			}
			if strings.Contains(m, "/tree/") || strings.HasSuffix(m, "/SKILL.md") {
				links[m] = true
			}
		}
		if len(links) != 1 {
			return "", nil, errors.New("marketplace page has no unambiguous GitHub skill source; use its source link")
		}
		for link := range links {
			link = strings.Replace(link, "/blob/", "/tree/", 1)
			link = strings.TrimSuffix(link, "/SKILL.md")
			return SourceContext(ctx, link)
		}
	}
	if host == "skills.sh" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 3 && parts[0] != "p" {
			root, cleanup, e := SourceContext(ctx, "https://github.com/"+parts[0]+"/"+parts[1]+".git")
			if e != nil {
				return "", nil, e
			}
			if e = ValidateName(parts[2]); e != nil {
				cleanup()
				return "", nil, e
			}
			b, _ := json.Marshal([]string{parts[2]})
			if e = os.WriteFile(filepath.Join(root, ".skillverk-selection.json"), b, 0600); e != nil {
				cleanup()
				return "", nil, e
			}
			return root, cleanup, nil
		}
	}
	if strings.HasSuffix(u.Path, "/SKILL.md") {
		b, e := fetchSkillBytes(ctx, value)
		if e != nil {
			return "", nil, e
		}
		tmp, e := TempDir("skill-md-*")
		if e != nil {
			return "", nil, e
		}
		cleanup := func() { _ = os.RemoveAll(tmp) }
		if e = os.WriteFile(filepath.Join(tmp, "SKILL.md"), b, 0644); e != nil {
			cleanup()
			return "", nil, e
		}
		return tmp, cleanup, nil
	}
	return wellKnownSource(ctx, value)
}

func checkDownloadTree(root string) error {
	var size int64
	count := 0
	return filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			info, e := d.Info()
			if e != nil {
				return e
			}
			size += info.Size()
			count++
		}
		if size > downloadLimit || count > archiveFileLimit {
			return errors.New("expanded skill collection exceeds limits")
		}
		return nil
	})
}

func flattenSkillArchive(root string) error {
	nested := unwrapArchive(root)
	if nested == root {
		return nil
	}
	stage, e := os.MkdirTemp(filepath.Dir(root), ".normalize-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	entries, e := os.ReadDir(nested)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if e = os.Rename(filepath.Join(nested, entry.Name()), filepath.Join(stage, entry.Name())); e != nil {
			return e
		}
	}
	if e = os.RemoveAll(root); e != nil {
		return e
	}
	return os.Rename(stage, root)
}

// Referenced artifacts must not downgrade an HTTPS collection to HTTP.
func fetchReferencedSkillBytes(ctx context.Context, source, target string) ([]byte, error) {
	from, err := url.Parse(source)
	if err != nil {
		return nil, err
	}
	to, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if from.Scheme == "https" && to.Scheme != "https" {
		return nil, errors.New("skill artifact must preserve HTTPS")
	}
	return fetchSkillBytes(ctx, target)
}
