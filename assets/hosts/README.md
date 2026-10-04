# Host adapters

TSP is the current product name. The legacy `/tpp` command and `tpp` extension component
remain intentional compatibility surfaces: the component ID is `tpp`, and the installed
path stays `~/.pi/agent/extensions/tpp/index.ts` to preserve existing installation state.

The decisions live in the binary. A host adapter is transport: it answers when the agent
finishes, hands the binary the working directory, and puts the answer somewhere a person or a
model will read it.

Three shapes cover every host seen so far.

`tsp sync` installs the embedded skills into every host configuration directory it finds: Claude
Code reads `~/.claude/skills`, OpenCode reads `~/.config/opencode/skills`, Gemini reads
`~/.gemini/skills`, and Codex reads `~/.codex/skills`. Discovery never offers Pi — the Pi extension
is the explicit `tsp sync --hosts pi` opt-in below. The Stop hook is wired only where its transport
is known—Claude Code's `~/.claude/settings.json`; OpenCode, Gemini, and Codex receive the skills, but
their transports are documented rather than wired by `sync`.

**Command hooks.** The host runs a command when the agent stops and reads JSON back. Claude Code
and Pi both work this way, and Pi's payload carries the same fields Claude's does
(`session_id`, `transcript_path`, `cwd`, `hook_event_name`, `stop_hook_active`), so
`tsp gate` serves both. Wire it with `tsp sync` on Claude Code, or by merging
`pi/settings.stop-hook.json` into Pi's settings — Pi's is contract-read, not run here (see Verified
below).

**Plugins.** The host loads code and gives it an event bus. OpenCode works this way, but its
automatic transport is disabled until session-scoped evidence is available: `opencode/tsp.ts`
no longer runs a check on `session.idle`, because a repo-wide check with no session binding
behind it fails closed. The file remains a harmless adapter export so an already-copied
plugin keeps loading, and on its own it shows no notices. If you copied the old plugin into
`.opencode/plugin/tpp.ts` or `~/.config/opencode/plugin/`, replace or remove that copy by
hand—`tsp sync` installs skills but does not manage copied plugin files. Manual use is
unaffected: run `tsp check` from a shell to see what the change still owes, plan gaps
included.

**Extensions.** The host loads an extension file and calls back into it. Pi works this way:
`pi/tsp.ts` registers native `/tsp` and backward-compatible `/tpp` commands (`check`, `feedback --summary`, `doctor`) through the same dispatcher, spawning the `tsp` CLI — it adds no skills and none of Gentle's surfaces (ODD, Engram, review lifecycle remain Gentle's). `tsp sync --hosts pi` is the only installer for it and writes just one file,
`~/.pi/agent/extensions/tpp/index.ts` — the destination stays `tpp` to preserve recorded installation state, sync, and uninstall behavior; never skills, never hook settings. The Pi Stop hook is a
separate, unverified surface (its row below is contract-read, not run here).

A host with neither still gets everything except "what did THIS session do": `tsp check`
reads git and the persisted plan and needs no host at all, which is also what makes it usable
from a Makefile, a pre-push script, or CI.

## Verified

| Host | Transport | Verified here |
|---|---|---|
| Claude Code | Stop command hook | yes: wired, run, exit code checked by `tsp doctor` |
| Anything with a shell | `tsp check` | yes |
| Pi | native `/tsp` command and `/tpp` compatibility alias via the extension (`sync --hosts pi` → `~/.pi/agent/extensions/tpp/index.ts`); Stop command hook, Claude-compatible payload | `/tsp` and `/tpp` dispatch covered by `pi/tsp.test.mjs` (run in CI); Stop hook contract read from the pi-hooks package; not run here |
| OpenCode | plugin event, disabled (adapter export only) | contract read from the OpenCode plugin docs; not run here |
| Codex, Gemini CLI | not implemented | their payload and output schemas are not verified here |

"Not run here" means exactly that: the shape comes from each project's own documentation, and
nobody has watched it fire on this machine. A hook that looks wired and never answers is the
failure this project keeps finding, so treat every row carrying that phrase as ready to test, not as
working.
