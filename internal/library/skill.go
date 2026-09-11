package library

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func ReadSkill(directory string) (Skill, error) {
	if info, err := os.Stat(directory); err == nil && info.Mode().IsRegular() {
		return readSkillFile(directory)
	}
	return readSkillFile(filepath.Join(directory, "SKILL.md"))
}
func readSkillFile(path string) (Skill, error) {
	directory := filepath.Dir(path)
	if filepath.Base(path) != "SKILL.md" {
		directory = path
	}
	f, err := os.Open(path)
	if err != nil {
		return Skill{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil {
		return Skill{}, err
	}
	if len(raw) > 1024*1024 {
		return Skill{}, fmt.Errorf("%s exceeds the 1 MiB preview limit", path)
	}
	if filepath.Base(path) != "SKILL.md" {
		raw, err = normalizeFlatEntry(raw, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
		if err != nil {
			return Skill{}, err
		}
	}
	lines := strings.Split(strings.TrimPrefix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\ufeff"), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return Skill{}, fmt.Errorf("%s needs YAML frontmatter with name and description", path)
	}
	end := 0
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == 0 {
		return Skill{}, fmt.Errorf("%s has no closing frontmatter marker", path)
	}
	var skill Skill
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &skill); err != nil {
		return skill, fmt.Errorf("%s: %w", path, err)
	}
	if err := ValidateName(skill.Name); err != nil {
		return skill, err
	}
	if strings.TrimSpace(skill.Description) == "" {
		return skill, fmt.Errorf("%s needs a description", path)
	}
	skill.descriptionCharacters = utf8.RuneCountInString(skill.Description)
	skill.Description = strings.Join(strings.Fields(Clean(skill.Description)), " ")
	skill.Path = directory
	return skill, nil
}

// Flat formats use their filename as identity. Preserve the body and metadata;
// add a portable entry name and a description when the host format omitted them.
func normalizeFlatEntry(raw []byte, name string) ([]byte, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	text := strings.TrimPrefix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\ufeff")
	fields := map[string]any{}
	body := text
	if strings.HasPrefix(text, "---\n") {
		lines := strings.Split(text, "\n")
		end := 0
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				end = i
				break
			}
		}
		if end == 0 {
			return nil, fmt.Errorf("flat skill has no closing frontmatter marker")
		}
		if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fields); err != nil {
			return nil, err
		}
		body = strings.Join(lines[end+1:], "\n")
	}
	fields["name"] = name
	if description, ok := fields["description"].(string); !ok || strings.TrimSpace(description) == "" {
		fields["description"] = "Use the " + name + " workflow."
	}
	front, err := yaml.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + string(front) + "---\n" + body), nil
}
