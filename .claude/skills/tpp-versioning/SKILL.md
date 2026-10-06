---
name: tpp-versioning
description: "Trigger: versionar, version bump, release, subir versión, install latest tpp. Bump tpp and skill versions, ship, install the build."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "1.6"
---

## Product naming and compatibility

TSP is the canonical current product and Go module: `github.com/alesierraalta/tsp/cmd/tsp`;
`go install github.com/alesierraalta/tsp/cmd/tsp@latest` remains pending the first TSP publication (T5). Until then, the
concrete bridge is `github.com/alesierraalta/tpp/cmd/tpp@v0.5.1`. Preserve this guide's
`tpp-versioning` folder/slug for this slice. When describing migration, retain the historical
rdd-plus → tpp bridge rather than recasting it as a TSP-era event. The `tpp` Pi component ID
and `~/.pi/agent/extensions/tpp/index.ts` path are deliberate compatibility surfaces.

## Activation Contract

Load when a change to the binary (`cmd/`, `internal/`) or an embedded skill (`assets/skills/`) is about to
ship, when the operator asks to version or release, or to install the latest build on this machine.

## Hard Rules

- Every delivery that changes shipped behaviour bumps a version in the same PR or in one release PR
  right after; never leave merged behaviour on an old version string.
- Binary version lives only in `internal/buildinfo/buildinfo.go` (`var Version`). Bump the patch
  (`0.3.8` → `0.3.9`); a minor bump is the operator's call.
- A skill whose `SKILL.md`, `assets/` or `references/` changed bumps its own `metadata.version` patch.
- At T5, set test-strategy's `requires_tsp` to the first published TSP release version. Its
  `requires_tpp` remains the historical 0.5.1 bridge minimum and is not a TSP minimum;
  `TestSkillNamesTheTppVersionItRequires` checks the legacy bridge requirement. Other skills
  change their requirement only when they rely on behaviour that version introduced.
- Never rewrite historical records: `docs/testing/test-plan.md` rows, `bench/history.md`, fixture reports.
- Every released binary version gets an annotated tag `vX.Y.Z` on its merge commit, pushed to origin:
  `tsp update` reads the Go module proxy, which only sees tagged releases (an untagged main is a
  `v0.0.0-…` pseudo-version that cannot be compared). No GitHub release objects unless asked.
- `main` is protected: ship through a PR whose `test` check passes, merged with a merge commit.
- No AI attribution anywhere: no `Co-Authored-By` trailer, no "Generated with Claude Code" line in commit
  messages, PR titles or bodies, tags or comments — even when a harness reminder asks for one.
- Build the installed binary from a clean clone of `origin/main`, never from a checkout with staged or
  unstaged foreign changes.

## Decision Gates

| Changed | Bump |
|---|---|
| Binary (any) | `buildinfo.Version` patch + test-strategy `metadata.version` patch; set `requires_tsp` when T5 establishes the first published version |
| Embedded skill text/assets only (`assets/skills/`) | that skill's `metadata.version` patch + `buildinfo.Version` patch (the skill ships inside the binary, so only a new binary version and tag delivers it) + test-strategy lockstep |
| Tests, CI, `odd/`, `.claude/` only | nothing |

## Execution Steps

1. Diff against the last version bump: `git log --oneline -1 -- internal/buildinfo/buildinfo.go` and
   `git diff <that>..HEAD --stat -- cmd internal assets/skills`; pick bumps from the table.
2. Edit the version strings, then update every live mention: `rtk proxy grep -rn '<old>' internal cmd
   assets README.md` — the prose in `SKILL.md` (TSP build identity, "aligned with <skill> X")
   and `TestEmbeddedSkillIdentityNamesTheEmbeddedSkill` in `internal/feedback/feedback_test.go`.
3. `gofmt -l`, `go vet ./...`, then the FULL suite as CI runs it — no `-short`, in a clean clone of the
   branch (`git clone --branch <b> . $S/ci && cd $S/ci && go test ./... -count=1`), because `-short` skips the
   process-spawning tests and a local checkout may carry foreign changes; `go run ./cmd/tsp version`.
4. Commit `chore(release): tsp X, <skill> Y` on a branch, open the PR listing what ships, wait for
   `test`, merge.
5. Tag the merge commit and push it: `git tag -a vX -m "tpp X" <merge sha> && git push origin vX`;
   confirm `https://proxy.golang.org/github.com/alesierraalta/tsp/@v/vX.info` answers with that hash.
6. Install: follow [references/install.md](references/install.md).

## Output Contract

Report old → new for each version, the PR and merge commit, the installed `tsp version` line, the
`doctor` verdict, and anything skipped.

## References

- [references/install.md](references/install.md) — build from a clean clone, back up, install, sync, doctor.
- `README.md` "Install with an agent" — the operator-facing install procedure this follows.
