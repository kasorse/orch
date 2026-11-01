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
