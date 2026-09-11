package library

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range files {
		h := &zip.FileHeader{Name: name}
		h.SetMode(0755)
		f, e := z.CreateHeader(h)
		must(t, e)
		_, e = f.Write([]byte(body))
		must(t, e)
	}
	must(t, z.Close())
	return b.Bytes()
}

const remoteSkill = "---\nname: client\ndescription: Download fixture\n---\nRead resources/data.bin\n"

func TestArchiveImportAndRefreshPreservesResources(t *testing.T) {
	s, root, _ := fixture(t)
	bundle := filepath.Join(root, "bundle.zip")
	put(t, bundle, string(testZip(t, map[string]string{"package/client/SKILL.md": remoteSkill, "package/client/resources/data.bin": "\x00\xff", "package/client/scripts/run.sh": "fixture"})))
	c, e := OpenCollection(bundle)
	must(t, e)
	defer c.Close()
	entries, e := s.Publish(c, nil, false)
	must(t, e)
	entry := entries["client"]
	readSibling(t, filepath.Join(s.entryPath(entry), "resources", "data.bin"), "\x00\xff")
	info, e := os.Stat(filepath.Join(s.entryPath(entry), "scripts", "run.sh"))
	must(t, e)
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		t.Fatal("executable mode lost")
	}
	put(t, bundle, string(testZip(t, map[string]string{"package/client/SKILL.md": remoteSkill, "package/client/resources/data.bin": "new", "package/client/scripts/run.sh": "fixture"})))
	_, e = s.Refresh([]string{"client"})
	must(t, e)
	readSibling(t, filepath.Join(s.entryPath(entry), "resources", "data.bin"), "new")
}
func TestArchiveRefusesTraversalDuplicatesAndLinks(t *testing.T) {
	for _, files := range []map[string]string{{"../outside": "bad"}, {"/outside": "bad"}, {"a\\outside": "bad"}, {"A": "one", "a": "two"}} {
		root := tempDir(t)
		if e := unpackSkills(context.Background(), testZip(t, files), root); e == nil {
			t.Fatal("unsafe archive accepted", files)
		}
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "link"}
	h.SetMode(os.ModeSymlink | 0777)
	f, e := z.CreateHeader(h)
	must(t, e)
	_, e = f.Write([]byte("outside"))
	must(t, e)
	must(t, z.Close())
	if e = unpackSkills(context.Background(), b.Bytes(), tempDir(t)); e == nil {
		t.Fatal("symlink archive accepted")
	}
}
func TestWellKnownFilesAndArtifacts(t *testing.T) {
	for _, artifact := range []bool{false, true} {
		t.Run(fmt.Sprint(artifact), func(t *testing.T) {
			data := testZip(t, map[string]string{"SKILL.md": remoteSkill, "resources/data.bin": "binary\x00"})
			sum := sha256.Sum256(data)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/pack/.well-known/agent-skills/index.json":
					if artifact {
						fmt.Fprintf(w, `{"skills":[{"name":"client","type":"archive","url":"bundle.zip","digest":"sha256:%x"}]}`, sum)
					} else {
						fmt.Fprint(w, `{"skills":[{"name":"client","files":["SKILL.md","resources/data.bin"]}]}`)
					}
				case "/pack/.well-known/agent-skills/bundle.zip":
					w.Write(data)
				case "/pack/.well-known/agent-skills/client/SKILL.md":
					fmt.Fprint(w, remoteSkill)
				case "/pack/.well-known/agent-skills/client/resources/data.bin":
					w.Write([]byte("binary\x00"))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			c, e := OpenCollection(server.URL + "/pack")
			must(t, e)
			defer c.Close()
			if len(c.Skills) != 1 {
				t.Fatal(c.Skills)
			}
			readSibling(t, filepath.Join(c.Skills[0].Path, "resources", "data.bin"), "binary\x00")
		})
	}
}
func TestWellKnownMissingResourceDigestScopeAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/agent-skills/index.json":
			fmt.Fprint(w, `{"skills":[{"name":"client","files":["SKILL.md","missing.bin"]}]}`)
		case "/.well-known/agent-skills/client/SKILL.md", "/bad/.well-known/client/SKILL.md":
			fmt.Fprint(w, remoteSkill)
		case "/bad/.well-known/agent-skills/index.json":
			fmt.Fprint(w, `{"skills":[{"name":"client","type":"skill-md","url":"../client/SKILL.md","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, path := range []string{"", "/missing-scope", "/bad"} {
		c, e := OpenCollection(server.URL + path)
		if e == nil {
			c.Close()
			t.Fatal("incomplete source accepted", path)
		}
		if path == "/bad" && !strings.Contains(e.Error(), "digest mismatch") {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e := SourceContext(ctx, server.URL); e != context.Canceled {
		t.Fatal(e)
	}
}
func TestRuntimeAndTutorialRequirements(t *testing.T) {
	s, root, _ := fixture(t)
	src := skill(t, root, "client", "This example teaches plugin commands: `${CLAUDE_PLUGIN_ROOT}/scripts/example.sh`.")
	req, e := inspectRequirements(context.Background(), src, "client")
	must(t, e)
	if len(req.Plugins) != 0 || len(req.Notes) == 0 {
		t.Fatal(req)
	}
	c, e := OpenCollection(src)
	must(t, e)
	defer c.Close()
	_, e = s.Publish(c, nil, false)
	must(t, e)
	r := repo(t, root, "project")
	_, e = s.Select(r, []string{"client"}, true)
	must(t, e)
	put(t, filepath.Join(src, "SKILL.md"), "---\nname: client\ndescription: Fixture\nmetadata:\n  openclaw:\n    requires:\n      bins: [skillverk-fixture-missing-binary]\n---\nRun tool\n")
	req, e = inspectRequirements(context.Background(), src, "client")
	must(t, e)
	if e = req.check("client", src); e == nil || !strings.Contains(e.Error(), "skillverk-fixture-missing-binary") {
		t.Fatal(e)
	}
	put(t, filepath.Join(src, "SKILL.md"), "---\nname: client\ndescription: Fixture\n---\nRun {baseDir}/scripts/tool.py")
	req, e = inspectRequirements(context.Background(), src, "client")
	must(t, e)
	if e = req.check("client", src); e == nil || !strings.Contains(e.Error(), "{baseDir}") {
		t.Fatal(e)
	}
}

func TestRegistryArchiveHandoff(t *testing.T) {
	archive := testZip(t, map[string]string{"repo/skills/client/SKILL.md": remoteSkill, "repo/skills/client/data.txt": "payload"})
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/download" {
			fmt.Fprintf(w, `{"sourceRef":"public-github","archiveUrl":%q,"path":"skills/client"}`, endpoint+"/repo.zip")
			return
		}
		w.Write(archive)
	}))
	defer server.Close()
	endpoint = server.URL
	c, err := OpenCollection(endpoint + "/api/v1/download")
	must(t, err)
	defer c.Close()
	selected, err := c.Select(nil)
	must(t, err)
	if len(selected) != 1 || selected[0].Name != "client" {
		t.Fatal(selected)
	}
	readSibling(t, filepath.Join(selected[0].Path, "data.txt"), "payload")
}
func TestArtifactCannotDowngradeHTTPS(t *testing.T) {
	_, err := fetchReferencedSkillBytes(context.Background(), "https://example.com/index.json", "http://127.0.0.1/file")
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatal(err)
	}
}

func TestSourceRateLimitRetryAndCancellation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(429)
		} else if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
		} else {
			fmt.Fprint(w, remoteSkill)
		}
	}))
	defer server.Close()
	body, err := fetchSkillBytes(context.Background(), server.URL)
	must(t, err)
	if string(body) != remoteSkill || calls.Load() != 2 {
		t.Fatal(string(body), calls.Load())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = fetchSkillBytes(ctx, server.URL+"/wait")
	if err != context.DeadlineExceeded {
		t.Fatal(err)
	}
}

func TestRuntimeMetadataJSONString(t *testing.T) {
	var requirements Requirements
	inspectRuntimeMetadata("---\nname: client\nmetadata: >-\n  {\"openclaw\":{\"requires\":{\"bins\":[\"fixture-tool\"]}}}\n---\nBody", &requirements)
	if len(requirements.Bins) != 1 || requirements.Bins[0] != "fixture-tool" {
		t.Fatal(requirements)
	}
}
