---
id: AT12
depends_on: [AT07, AT08]
---
# AT12 — skills under `user_settings`: discovery and the `skill` tool

**Size** M

## Goal

SPEC.md §5.4 and §2 item 8.

## Files

- `internal/provider/tools/skills.go`: discover `~/.claude/skills/*/SKILL.md` and the enabled
  plugins' skills (`settings.json` `enabledPlugins`, `plugins/installed_plugins.json`), the front
  matter's `name`/`description`, names unique (a duplicate is skipped with a warning); the `skill`
  tool with `name` (enum) and `path`; reads under the skill's directory with §5's rules.
- The home directory is passed in by `app` (`os.UserHomeDir`), so tests use a temporary one.
- Tests: discovery, a plugin disabled, a path escaping the skill directory, a deny-listed file.
- README "Extensions": what `user_settings` gives under each transport.

## Acceptance

- Without `user_settings` for the action no `skill` tool is offered and nothing under `~/.claude`
  is read.
- `scripts/check.sh` passes.

## As built

2026-11-03.

- `internal/provider/tools/skills.go`: `DiscoverSkills(home)` returns the skills and the notes
  (a skipped skill, an unreadable settings file, an enabled plugin without a user install, and
  once that hooks do not run); `SkillTool(skills, deny)` is the `skill` tool (`name` an enum,
  `path` nullable, strict-ready). A skill without front matter is named after its directory and
  described by its first line of text, as the claude CLI lists it (`tailor-cv: Tailor CV`); a
  directory without `SKILL.md` (`~/.claude/skills/synced`) is not a skill. The front matter is
  parsed with `yaml.v3` (folded descriptions are common), the module's one dependency; the
  package's import test allows it, since it is neither a command nor a connection.
- Discovery runs once, in `app` (`registerSkills`, after the credentials), not per call: the
  notes would otherwise repeat on every step. It runs only when an action with `user_settings`
  is routed to a provider over the API transport (`config.UserSettingsOverAPI`); the clients get
  the list through `SetSkills` and offer it only when the call's `Extensions.UserSettings` is
  set. The run prints the skills it offers.
- Offered to `read` and `edit` calls only, not to `web` (research) or `none`: a web call has a
  way out, and a skill directory is the operator's files. Added to SPEC §5.4.
- `user_settings` with codex was refused for every action; it is now refused only for an action
  that has it on the codex **CLI**, and allowed over the codex API transport (the same tool).
- A refused skill path gets one message ("not a readable file of the skill; use a path relative
  to its directory") instead of the repository-worded refusal of `Root`.
- `shortcut:` only a plugin's `skills/` directory is read, not custom skill paths in its
  `.claude-plugin/plugin.json`.
- No integration scenario: the loop is the one AT07/AT08 test, and the unit tests run the skill
  call through both clients' requests; discovery was also checked once against a real
  `~/.claude` (3 skills, 6 of an enabled plugin, the hooks note).
