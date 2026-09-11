package onboarding

import (
	"errors"
	"fmt"
	"os/exec"
)

// Resolve prefers Codex when both CLIs are installed. An explicit choice never
// silently switches providers; auto uses local review when neither is installed.
func Resolve(provider string) string {
	if provider != "" && provider != "auto" {
		return provider
	}
	for _, name := range []string{"codex", "claude"} {
		if _, e := exec.LookPath(name); e == nil {
			return name
		}
	}
	return "local"
}

// ModelCommand inherits authentication and provider configuration. An empty
// model leaves the provider's own model selection unchanged.
func ModelCommand(provider, model, instructions, directory string) (*exec.Cmd, error) {
	provider = Resolve(provider)
	if provider != "codex" && provider != "claude" {
		return nil, errors.New("choose codex or claude")
	}
	path, e := exec.LookPath(provider)
	if e != nil {
		return nil, fmt.Errorf("%s is not installed on PATH; review the plan in Skillverk: %w", provider, e)
	}
	args := []string{}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, Prompt(instructions))
	cmd := exec.Command(path, args...)
	cmd.Dir = directory
	return cmd, nil
}
