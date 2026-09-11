package main

import (
	"encoding/json"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func cliFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if e := os.Mkdir(repo, 0755); e != nil {
		t.Fatal(e)
	}
	if out, e := exec.Command("git", "init", "-q", repo).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	src := filepath.Join(root, "source")
	if e := os.Mkdir(src, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: probe\ndescription: CLI fixture\n---\nNo workflow.\n"), 0644); e != nil {
		t.Fatal(e)
	}
	return filepath.Join(root, "library"), repo, src
}
func capture(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	f, e := os.CreateTemp(t.TempDir(), "output")
	if e != nil {
		t.Fatal(e)
	}
	old := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = old; f.Close() }()
	err := fn()
	_, e = f.Seek(0, 0)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(f)
	if e != nil {
		t.Fatal(e)
	}
	return string(raw), err
}
func TestCLILifecycleAndPartialJSON(t *testing.T) {
	home, repo, src := cliFixture(t)
	base := []string{"--home", home, "--directory", repo}
	invoke := func(args ...string) (string, error) {
		return capture(t, func() error { return run(append(append([]string{}, base...), args...)) })
	}
	if _, e := invoke("add", src); e != nil {
		t.Fatal(e)
	}
	output, e := invoke("list", "--json")
	if e != nil {
		t.Fatal(e)
	}
	var data struct {
		Skills []struct {
			Name     string
			Selected bool
		}
	}
	if e = json.Unmarshal([]byte(output), &data); e != nil {
		t.Fatal(output, e)
	}
	found := false
	for _, sk := range data.Skills {
		if sk.Name == "probe" {
			found = true
			if sk.Selected {
				t.Fatal("import activated")
			}
		}
	}
	if !found {
		t.Fatal("not listed")
	}
	if e = os.WriteFile(filepath.Join(repo, ".claude"), []byte("conflict"), 0644); e != nil {
		t.Fatal(e)
	}
	output, e = invoke("on", "probe", "--json")
	if e == nil {
		t.Fatal("partial returned success")
	}
	var result struct {
		OK      bool
		Results []struct{ Agent, Error string }
	}
	if er := json.Unmarshal([]byte(output), &result); er != nil {
		t.Fatal(output, er)
	}
	if result.OK || len(result.Results) != 2 || result.Results[0].Error != "" || result.Results[1].Error == "" {
		t.Fatal(output)
	}
	if e = os.Remove(filepath.Join(repo, ".claude")); e != nil {
		t.Fatal(e)
	}
	if _, e = invoke("list"); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Lstat(filepath.Join(repo, ".claude", "skills", "probe")); !os.IsNotExist(e) {
		t.Fatal("list retried activation")
	}
	if _, e = invoke("retry"); e != nil {
		t.Fatal(e)
	}
	if _, e = invoke("agents", "claude"); e != nil {
		t.Fatal(e)
	}
	if _, e = invoke("delete", "probe", "--json"); e == nil {
		t.Fatal("unconfirmed deletion accepted")
	}
	if _, e = invoke("delete", "probe", "--yes", "--json"); e != nil {
		t.Fatal(e)
	}
}
func TestCutoverRejectsObsoleteCommandsAndFlags(t *testing.T) {
	home, repo, _ := cliFixture(t)
	for _, args := range [][]string{{"sync"}, {"demo"}, {"on", "probe", "--agent", "codex"}, {"on", "probe", "--dry-run"}, {"--project", repo, "list"}} {
		_, e := capture(t, func() error { return run(append([]string{"--home", home, "--directory", repo}, args...)) })
		if e == nil {
			t.Fatal("obsolete operation accepted", args)
		}
	}
	root, _ := newRoot()
	var text strings.Builder
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		text.WriteString(c.Use + "\n" + c.Short + "\n" + c.Long + "\n" + c.Example + "\n")
		c.Flags().VisitAll(func(f *pflag.Flag) { text.WriteString("--" + f.Name + " " + f.Usage + "\n") })
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	for _, word := range []string{"--dry-run", "--agent ", "sync ", "pinned versions", ".skillverk.json"} {
		if strings.Contains(text.String(), word) {
			t.Fatal("obsolete help", word)
		}
	}
}

// Cobra accepts flags before, between, and after positional arguments, and
// repeated or comma-separated --skill values select several entries at once.
func TestFlagsInterspersedWithArguments(t *testing.T) {
	home, repo, _ := cliFixture(t)
	src := filepath.Join(filepath.Dir(repo), "many")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		dir := filepath.Join(src, name)
		if e := os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
		body := "---\nname: " + name + "\ndescription: fixture " + name + "\n---\nNo workflow.\n"
		if e := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0644); e != nil {
			t.Fatal(e)
		}
	}
	_, e := capture(t, func() error {
		return run([]string{"add", src, "--skill", "alpha", "--skill=beta", "--home", home, "--directory", repo})
	})
	if e != nil {
		t.Fatal(e)
	}
	output, e := capture(t, func() error { return run([]string{"--home", home, "list", "--json", "--directory", repo}) })
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{`"alpha"`, `"beta"`} {
		if !strings.Contains(output, want) {
			t.Fatal("missing", want, output)
		}
	}
	if strings.Contains(output, `"gamma"`) {
		t.Fatal("imported an entry that was not selected")
	}
}

func TestCLISelectsMultipleHarnesses(t *testing.T) {
	home, repo, src := cliFixture(t)
	base := []string{"--home", home, "--directory", repo}
	invoke := func(args ...string) (string, error) {
		return capture(t, func() error { return run(append(append([]string{}, base...), args...)) })
	}
	if _, e := invoke("add", src); e != nil {
		t.Fatal(e)
	}
	if _, e := invoke("agents", "opencode,cursor", "gemini"); e != nil {
		t.Fatal(e)
	}
	if _, e := invoke("on", "probe"); e != nil {
		t.Fatal(e)
	}
	for _, dir := range []string{".opencode", ".cursor", ".gemini"} {
		if _, e := os.Stat(filepath.Join(repo, dir, "skills", "probe", "SKILL.md")); e != nil {
			t.Fatal(e)
		}
	}
	output, e := invoke("agents", "--json")
	if e != nil || !strings.Contains(output, `"opencode"`) {
		t.Fatal(output, e)
	}
	if _, e := invoke("agents", "both"); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Lstat(filepath.Join(repo, ".opencode", "skills", "probe")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}
