package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// Requirements describe literal installation assumptions. They are not a claim
// that static inspection discovers every runtime dependency.
type Requirements struct {
	Notes       []string `json:"notes,omitempty"`
	Runtimes    []string `json:"runtimes,omitempty"`
	Bins        []string `json:"bins,omitempty"`
	AnyBins     []string `json:"any_bins,omitempty"`
	Env         []string `json:"env,omitempty"`
	OS          []string `json:"os,omitempty"`
	GlobalPaths []string `json:"global_paths,omitempty"`
	Plugins     []string `json:"plugins,omitempty"`
}

var globalSkillPath = regexp.MustCompile(`(?:\$\{CODEX_HOME:-\$HOME/\.codex\}|\$\{CODEX_HOME\}|\$CODEX_HOME|\$\{CLAUDE_CONFIG_DIR\}|\$CLAUDE_CONFIG_DIR|~/(?:\.codex|\.claude|\.agents)|\$HOME/(?:\.codex|\.claude|\.agents))/skills/[a-z0-9-]+/[a-zA-Z0-9_./-]+`)
var pluginInvocation = regexp.MustCompile(`(?i)(?:use|invoke|load|understand|skill|sub-skill)[: ]+[\x60*]*([a-z][a-z0-9-]*):([a-z][a-z0-9-]*)`)
var pluginSkillName = regexp.MustCompile(`\b([a-z][a-z0-9-]*):([a-z][a-z0-9-]*)\b`)

func inspectRequirements(ctx context.Context, source, name string) (Requirements, error) {
	var out Requirements
	root, err := filepath.EvalSymlinks(source)
	if err != nil {
		return out, err
	}
	namespaces := map[string]bool{}
	var pluginRoots []string
	for parent := root; ; parent = filepath.Dir(parent) {
		for _, dir := range []string{".claude-plugin", ".codex-plugin", ".cursor-plugin", ".tessl-plugin", "."} {
			var manifest struct {
				Name      string `json:"name"`
				MCP       any    `json:"mcpServers"`
				Hooks     any    `json:"hooks"`
				Variables any    `json:"variables"`
			}
			if raw, e := os.ReadFile(filepath.Join(parent, dir, "plugin.json")); e == nil && json.Unmarshal(raw, &manifest) == nil {
				namespaces[manifest.Name] = true
				pluginRoots = append(pluginRoots, parent)
				if manifest.MCP != nil || manifest.Hooks != nil || manifest.Variables != nil || Exists(filepath.Join(parent, "mcp.json")) || Exists(filepath.Join(parent, "mcp_config.json")) || Exists(filepath.Join(parent, ".mcp.json")) {
					out.Plugins = appendUnique(out.Plugins, manifest.Name+" bundled tools or hooks")
				}
			}
		}
		var pkg struct {
			Pi           json.RawMessage `json:"pi"`
			Dependencies map[string]any  `json:"dependencies"`
		}
		if raw, e := os.ReadFile(filepath.Join(parent, "package.json")); e == nil && json.Unmarshal(raw, &pkg) == nil && len(pkg.Pi) > 0 {
			var components map[string]json.RawMessage
			if json.Unmarshal(pkg.Pi, &components) == nil && (len(pkg.Dependencies) > 0 || len(components["extensions"]) > 0) {
				out.Plugins = appendUnique(out.Plugins, "Pi package dependencies or extensions")
			}
		}
		if Exists(filepath.Join(parent, "gemini-extension.json")) {
			out.Plugins = appendUnique(out.Plugins, "Gemini extension")
		}
		if filepath.Dir(parent) == parent || Exists(filepath.Join(parent, ".git")) {
			break
		}
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md", ".sh", ".py", ".js", ".cjs", ".mjs", ".json":
		default:
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(contextReader{ctx, f})
		err = errors.Join(err, f.Close())
		if err != nil {
			return err
		}
		body := string(raw)
		for _, ref := range globalSkillPath.FindAllString(body, -1) {
			ref = strings.TrimRight(ref, ".")
			// Record references to resources in this skill, not user output paths or
			// other tools whose installation is outside Skillverk's ownership.
			if !strings.Contains(ref, "/skills/"+name+"/") {
				continue
			}
			suffix := strings.SplitN(ref, "/skills/"+name+"/", 2)[1]
			resource := filepath.Join(root, filepath.FromSlash(suffix))
			if !within(resource, root) {
				continue
			}
			if _, err := os.Stat(resource); err != nil {
				continue
			}
			if !slices.Contains(out.GlobalPaths, ref) {
				out.GlobalPaths = append(out.GlobalPaths, ref)
			}
		}
		if path == filepath.Join(root, "SKILL.md") || path == root {
			inspectRuntimeMetadata(body, &out)
		}
		for _, variable := range []string{"CLAUDE_PLUGIN_ROOT", "CODEX_PLUGIN_ROOT", "CURSOR_PLUGIN_ROOT", "TESSL_PLUGIN_DIR", "extensionPath"} {
			if !strings.Contains(body, "$"+variable) && !strings.Contains(body, "${"+variable) {
				continue
			}
			required := false
			// A named variable alone may be teaching an API. Require evidence
			// of a resource outside the skill in the actual plugin package.
			for _, match := range pluginResource.FindAllStringSubmatch(body, -1) {
				for _, parent := range pluginRoots {
					resource := filepath.Join(parent, filepath.FromSlash(match[1]))
					if within(resource, parent) && !within(resource, root) && Exists(resource) {
						required = true
					}
				}
			}
			if required {
				out.Plugins = appendUnique(out.Plugins, variable)
			} else {
				out.Notes = appendUnique(out.Notes, "Mentions "+variable+"; no required external plugin resource was confirmed")
			}
		}
		if strings.ToLower(filepath.Ext(path)) == ".md" {
			matches := pluginInvocation.FindAllStringSubmatch(body, -1)
			for _, m := range pluginSkillName.FindAllStringSubmatch(body, -1) {
				if namespaces[m[1]] {
					matches = append(matches, m)
				}
			}
			for _, m := range matches {
				// A namespace pointing to an actual sibling skill is a plugin invocation,
				// unlike arbitrary colon-separated prose and examples.
				if _, err := ReadSkill(filepath.Join(filepath.Dir(root), m[2])); err == nil {
					if !slices.Contains(out.Plugins, m[1]+":") {
						out.Plugins = append(out.Plugins, m[1]+":")
					}
				}
			}
		}
		return nil
	})
	if Exists(filepath.Join(root, "mcp.json")) {
		out.Runtimes = appendUnique(out.Runtimes, "skill-scoped MCP configuration")
	}
	slices.Sort(out.GlobalPaths)
	slices.Sort(out.Plugins)
	return out, err
}
func resolveGlobalPath(ref string) string {
	home, _ := os.UserHomeDir()
	codex := os.Getenv("CODEX_HOME")
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = filepath.Join(home, ".claude")
	}
	return filepath.FromSlash(strings.NewReplacer("${CODEX_HOME:-$HOME/.codex}", codex, "${CODEX_HOME}", codex, "$CODEX_HOME", codex, "${CLAUDE_CONFIG_DIR}", claude, "$CLAUDE_CONFIG_DIR", claude, "$HOME", home, "~/", home+"/").Replace(ref))
}
func (r Requirements) check(name, content string) error {
	if err := validateNativeMetadata(content); err != nil {
		return err
	}
	if len(r.Runtimes) > 0 {
		return fmt.Errorf("requires %s runtime features; keep this installation with that runtime", strings.Join(r.Runtimes, ", "))
	}
	if len(r.OS) > 0 && !slices.Contains(r.OS, runtime.GOOS) && !(runtime.GOOS == "windows" && slices.Contains(r.OS, "win32")) {
		return fmt.Errorf("requires operating system %s", strings.Join(r.OS, ", "))
	}
	for _, bin := range r.Bins {
		if _, e := exec.LookPath(bin); e != nil {
			return fmt.Errorf("required command is unavailable: %s", bin)
		}
	}
	if len(r.AnyBins) > 0 {
		found := false
		for _, bin := range r.AnyBins {
			if _, e := exec.LookPath(bin); e == nil {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("requires one command from: %s", strings.Join(r.AnyBins, ", "))
		}
	}
	for _, key := range r.Env {
		if os.Getenv(key) == "" {
			return fmt.Errorf("required environment variable is unavailable: %s", key)
		}
	}

	if len(r.Plugins) > 0 {
		return fmt.Errorf("requires plugin support (%s); keep this installation with its plugin manager", strings.Join(r.Plugins, ", "))
	}
	for _, ref := range r.GlobalPaths {
		path := resolveGlobalPath(ref)
		parts := strings.SplitN(ref, "/skills/"+name+"/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid global requirement %q", ref)
		}
		suffix := parts[1]
		expected := filepath.Join(content, filepath.FromSlash(suffix))
		actual, err := filepath.EvalSymlinks(path)
		target, te := filepath.EvalSymlinks(expected)
		if err != nil || te != nil || !samePath(actual, target) {
			return fmt.Errorf("requires global resource %s to point to this skill; preserve its existing global installation or use a portable skill", path)
		}
	}
	return nil
}
func (s *Store) verifyRequirements(name string, entry Entry) error {
	return entry.Requirements.check(name, s.entryPath(entry))
}

var pluginResource = regexp.MustCompile(`\$\{?[A-Za-z_][A-Za-z0-9_]*\}?/([A-Za-z0-9_./-]+)`)

func appendUnique(values []string, value string) []string {
	if !slices.Contains(values, value) {
		return append(values, value)
	}
	return values
}
func inspectRuntimeMetadata(body string, out *Requirements) {
	var fields struct {
		Compatibility string    `yaml:"compatibility"`
		Dispatch      string    `yaml:"command-dispatch"`
		Metadata      yaml.Node `yaml:"metadata"`
		MCP           yaml.Node `yaml:"mcpServers"`
		Platforms     []string  `yaml:"platforms"`
		Environment   []struct {
			Name string `yaml:"name"`
		} `yaml:"required_environment_variables"`
		Paths yaml.Node `yaml:"paths"`
	}
	parts := strings.SplitN(strings.ReplaceAll(body, "\r\n", "\n"), "---", 3)
	if len(parts) != 3 || yaml.Unmarshal([]byte(parts[1]), &fields) != nil {
		return
	}
	if fields.Compatibility != "" {
		out.Notes = appendUnique(out.Notes, "Compatibility: "+fields.Compatibility)
	}
	if fields.MCP.Kind != 0 {
		out.Runtimes = appendUnique(out.Runtimes, "skill-scoped MCP configuration")
	}
	if fields.Paths.Kind != 0 {
		out.Runtimes = appendUnique(out.Runtimes, "path-scoped skill discovery")
	}
	for _, platform := range fields.Platforms {
		if platform == "macos" {
			platform = "darwin"
		}
		out.OS = appendUnique(out.OS, platform)
	}
	for _, env := range fields.Environment {
		out.Env = appendUnique(out.Env, env.Name)
	}
	if strings.Contains(parts[2], "{baseDir}") {
		out.Runtimes = appendUnique(out.Runtimes, "host-provided {baseDir} substitution")
	}
	if fields.Dispatch == "tool" {
		out.Runtimes = appendUnique(out.Runtimes, "OpenClaw")
	}
	var metadata map[string]any
	if fields.Metadata.Kind == yaml.ScalarNode {
		_ = yaml.Unmarshal([]byte(fields.Metadata.Value), &metadata)
	} else {
		_ = fields.Metadata.Decode(&metadata)
	}
	if value, ok := metadata["hermes"]; ok {
		var hermes map[string]any
		raw, _ := yaml.Marshal(value)
		if yaml.Unmarshal(raw, &hermes) != nil {
			out.Runtimes = appendUnique(out.Runtimes, "Hermes")
		}
		for _, field := range []string{"config", "requires_toolsets", "requires_tools", "fallback_for_toolsets", "fallback_for_tools"} {
			if hermes[field] != nil {
				out.Runtimes = appendUnique(out.Runtimes, "Hermes "+field)
			}
		}
	}
	for _, key := range []string{"openclaw", "clawdbot"} {
		value, ok := metadata[key]
		if !ok {
			continue
		}
		var data []byte
		if text, ok := value.(string); ok {
			data = []byte(text)
		} else {
			data, _ = yaml.Marshal(value)
		}
		var config struct {
			OS       []string `yaml:"os"`
			Always   bool     `yaml:"always"`
			Requires struct {
				Bins    []string `yaml:"bins"`
				AnyBins []string `yaml:"anyBins"`
				Env     []string `yaml:"env"`
				Config  []string `yaml:"config"`
			} `yaml:"requires"`
			Nix any `yaml:"nix"`
		}
		if yaml.Unmarshal(data, &config) != nil {
			out.Runtimes = appendUnique(out.Runtimes, "OpenClaw")
			continue
		}
		out.OS = append(out.OS, config.OS...)
		if !config.Always {
			out.Bins = append(out.Bins, config.Requires.Bins...)
			out.AnyBins = append(out.AnyBins, config.Requires.AnyBins...)
			out.Env = append(out.Env, config.Requires.Env...)
			if len(config.Requires.Config) > 0 {
				out.Runtimes = appendUnique(out.Runtimes, "OpenClaw")
			}
		}
		if config.Nix != nil {
			out.Runtimes = appendUnique(out.Runtimes, "Nix plugin")
		}
	}
}
