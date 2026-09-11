package library

import (
	"fmt"
	"path/filepath"
)

// Native controls and known compatibility discovery paths share this registry.
// Compatibility paths are observations, never additional activation targets.
type harness struct {
	id, label, path, global string
	aliases                 []string
	defaultEnabled          bool
}

var harnessRegistry = []harness{
	{"codex", "Codex", ".agents/skills", "", nil, true},
	{"claude", "Claude Code", ".claude/skills", "", nil, true},
	{"opencode", "OpenCode", ".opencode/skills", ".config/opencode/skills", []string{".agents/skills", ".claude/skills"}, false},
	{"cursor", "Cursor", ".cursor/skills", ".cursor/skills", []string{".agents/skills", ".claude/skills", ".codex/skills"}, false},
	{"gemini", "Gemini CLI", ".gemini/skills", ".gemini/skills", []string{".agents/skills"}, false},
	{"copilot", "GitHub Copilot", ".github/skills", ".copilot/skills", nil, false},
	{"windsurf", "Windsurf", ".windsurf/skills", ".codeium/windsurf/skills", nil, false},
	{"factory", "Factory", ".factory/skills", ".factory/skills", []string{".agents/skills", ".agent/skills"}, false},
	{"pi", "Pi", ".pi/skills", ".pi/agent/skills", nil, false},
	{"vibe", "Vibe", ".vibe/skills", ".vibe/skills", nil, false},
	{"antigravity", "Antigravity", ".agent/skills", "", nil, false},
}
var Agents, DefaultAgents, agentPaths = func() ([]string, []string, map[string]string) {
	var all, defaults []string
	paths := map[string]string{}
	for _, h := range harnessRegistry {
		all = append(all, h.id)
		paths[h.id] = h.path
		if h.defaultEnabled {
			defaults = append(defaults, h.id)
		}
	}
	return all, defaults, paths
}()

// AgentPath is the repository-relative directory a harness reads skills from.
func AgentPath(agent string) string { return agentPaths[agent] }

func AgentLabel(agent string) string {
	for _, h := range harnessRegistry {
		if h.id == agent {
			return h.label
		}
	}
	return agent
}

// Validate the portable metadata contract before exposing imported content.
// Imports retain the original bytes so users can review and correct them.
func validateNativeMetadata(path string) error {
	sk, err := ReadSkill(path)
	if err != nil {
		return err
	}
	if sk.descriptionCharacters > 1024 {
		return fmt.Errorf("%s description has %d characters; native skill descriptions must fit within 1024 characters", sk.Name, sk.descriptionCharacters)
	}
	return nil
}

// Report matching readable entries, without claiming the harness loaded them.
func compatibilityPaths(repo, name string) map[string][]string {
	found := map[string][]string{}
	if repo == "" {
		return found
	}
	for _, h := range harnessRegistry {
		for _, alias := range h.aliases {
			path := filepath.Join(repo, filepath.FromSlash(alias), name)
			sk, e := ReadSkill(path)
			if e == nil && sk.Name == name {
				found[h.id] = append(found[h.id], path)
			}
		}
	}
	return found
}
