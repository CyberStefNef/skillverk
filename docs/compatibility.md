# Compatibility

Which harnesses and sources Skillverk works with, and what it leaves alone.

## Repository activation targets

One repository selection applies to any non-empty subset of these harnesses.
Codex and Claude Code are the defaults.

| Harness | Repository discovery directory |
| --- | --- |
| Codex | `.agents/skills` |
| Claude Code | `.claude/skills` |
| OpenCode | `.opencode/skills` |
| Cursor | `.cursor/skills` |
| Gemini CLI | `.gemini/skills` |
| GitHub Copilot | `.github/skills` |
| Windsurf | `.windsurf/skills` |
| Factory | `.factory/skills` |
| Pi | `.pi/skills` |
| Vibe | `.vibe/skills` |
| Antigravity | `.agent/skills` |

Disabling a harness removes only the links Skillverk manages for it. Each
harness still applies its own discovery rules, and several read directories
belonging to others. OpenCode, for example, reads `.agents/skills`, so it can
find a skill you selected for Codex alone.

Skill details list readable entries in the repository directories that OpenCode,
Cursor, Gemini, and Factory are known to share, and JSON output reports them as
`compatibility_paths`. Skillverk reports these paths separately from the links
it manages.

## Setup discovery

Setup inventories native installations for Codex, Claude Code, Cursor,
Antigravity, Hermes, Copilot, Gemini, OpenCode, Windsurf, Factory, Pi, Vibe,
and Amp, at both repository and user-account roots. It also reads
configured skill directories: Hermes YAML, Pi skill settings, OpenCode
JSON/JSONC, Amp settings, and Vibe `skill_paths` arrays. A configuration change
invalidates a reviewed plan.

Nested repository skills keep their directory scope. An approved migration
replaces existing native paths with recorded links and does not expose a nested
skill at the repository root.

## Sources

| Source | Support |
| --- | --- |
| Local directories and Markdown files | Directories preserved. Flat entries become SKILL.md bundles with the filename as ID, copying nested local references |
| Git repositories and subfolders | Shallow, blob-filtered fetch with sparse checkout. Pi `git:github.com/owner/repo@tag` shorthand accepts pinned refs |
| Archives | ZIP, tar, and Gemini `.skill`, with validated extraction and retained executable permissions |
| npm | Public `npm:package@version` with SHA-512 integrity verification, without installation hooks |
| HTTP collections | Well-known file indexes, `index.json`, digest-verified artifacts |
| Services | skills.sh skill links and public packs, SkillsMP pages resolving to one GitHub source, public ClawHub skill links |
| Manifests | Pi, Cursor, Tessl-plugin, and root Agent Plugin manifests select skill paths, including explicit hidden paths |

Skillverk can take over an installer record from Vercel, ClawHub, OpenSkills,
and Hermes Hub, and only for installations you approve during migration. A
failed handoff restores the records.

Skillverk recognizes runtime requirements declared in Amp MCP configuration,
Hermes requirements, Gemini extensions, Cursor and Tessl plugin manifests, root
Agent Plugin manifests, and Pi package dependencies. It preserves the bytes and
reports the dependency. It does not supply the server, hook, or placeholder
substitution the skill needs, so a host-dependent skill stays with its runtime.

When an import refuses a skill, it names the cause, either a broken source
reference or a detected runtime requirement.

## Known boundaries

**Not activation targets.** Cline, Kiro, Roo, Kilo, Qwen, and Goose have
documented skill directories that setup does not discover and the registry does
not activate. Continue appears in the Vercel installer's mapping but has no
adapter here. Each one needs discovery, ownership, activation, and native
validation added together.

**Manager-owned ecosystems.** Tessl tiles and plugin bundles, Pi package
settings and their update lifecycle, and private registries stay with their
owners. Hermes can edit a writable shared directory in place, so pointing it at
shared library content lets those edits reach every consumer. Setup review says
so before you approve. Skillverk does not initialize Git submodules, so a source
repository that declares them can look incomplete.

**Formats.** During setup, flat Markdown installations with adjacent resources
stay where they are, because a client may resolve those resources against the
original filename.

**Validation.** Skillverk's ASCII naming rules are stricter than some clients'.
Qwen accepts identifiers Skillverk rejects, and Skillverk reports that rather
than renaming an installation. It keeps a description longer than 1,024
characters for review, refuses to activate the skill, and never truncates.

**Client-side rules.** A harness decides for itself whether to load a skill it
can see. Permission settings, disabled-skill settings, local and global
precedence, and session reloads all apply. Skillverk creates the link and
reports what is on disk.
