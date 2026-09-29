# Proving code actually runs

Static tools (Knip, Vulture, `deadcode`, CodeGraph blast radius) produce CANDIDATES.
Their known weakness is triage, not detection: a symbol with no callers may be loaded
dynamically, named in a config file the analyzer cannot parse, registered by generated
code, or be public API for a consumer outside the repo. Treat their output as the start
of the work.

## The R chain — climb it, then state the highest rung PROVEN

| Rung | Question | Evidence that closes it |
|---|---|---|
| R0 | Does it exist and import in the prod build? | The prod entry point imports it without error |
| R1 | Is there a static path from an entry point? | The call chain, named hop by hop (CodeGraph) |
| R2 | Is it WIRED? | It appears in the composition root / DI container / route table / registry / event subscription |
| R3 | Does the deployment ENABLE it? | Flag on for the audience, env var set, migration applied, worker/cron actually running, config default in prod |
| R4 | Does an execution touch the line? | Coverage from a real run, a temporary counter, a log line, a trace span |
| R5 | Does it run in PRODUCTION? | A nonzero counter / log / span from prod traffic |

A claim of "this works" is only as strong as the highest rung proven. R1 is where most
reviews stop and where most theater survives: the call chain reads fine and the flag is
off. **The cheapest honest step is R4** — add a counter or a log at the line, drive the
real entry point, confirm it fires, then remove the probe or keep it (`silent-degradation`
argues for keeping it). At R5, absence of an observation means **not observed**, not
does-not-run; telemetry may be missing, filtered, sampled, or unavailable.

Write the rung in the report. "R2 proven, R3 unverified — the flag default in prod is
unknown" is a real, useful answer. A confident "it works" backed by R1 is not.

## Triage taxonomy — every unreachable candidate lands in exactly one

| Verdict | How you confirm it | Action |
|---|---|---|
| **Dead** | No static path, no dynamic lookup, no config mention, no external consumer | Propose deletion in a separate authorized change; a comment is not a fix |
| **Dynamic** | Reached via reflection, a plugin/registry decorator, `getattr`, a string-keyed dispatch, a framework hook, a serializer by name | Not dead. Add a test pinning the dynamic key, because a rename breaks it silently |
| **Config-referenced** | Its name appears in YAML/JSON/env/IaC/DB rows/a template | Not dead. The config is now part of its contract — grep the config repo too |
| **Public API** | Exported and consumed outside this repo | Not dead. Deprecate with a version, never delete silently |
| **Never wired** | Exists, tested, and no path reaches it: nobody constructs it, no route mounts it, the flag was never turned on | **The core finding of this skill.** It was merged and never released |

### Unproven is a first-class verdict

Use `unproven` when an external consumer, generated registration, production configuration,
or required environment cannot be verified. It is evidence of neither dead nor not-dead;
record confidence, unknowns, and the next proof.

Search wide before declaring anything dead: the symbol name as a STRING (config, SQL,
templates, IaC, migrations, feature-flag definitions, generated manifests, other repos), not just as an
identifier. A grep restricted to source files is how a dynamically-loaded class gets
deleted.

## Deployment reality — R3 in detail

Merged is not running. Check each, per environment, and say which environment you checked:

- **Feature flag**: what is the DEFAULT in prod, for which audience, and who owns it?
  Flags decouple deploy from release by design; a merged flag that was never turned on is
  code that has never run. "Zombie flags" — logically dead branches left embedded — are a
  documented, common outcome.
- **Env / config**: is the variable actually set in the deployed environment, or is the
  code silently taking its default? Observe it safely (presence, source, allowed state, or a non-reversible hash); never print secret values.
- **Wiring**: the class is constructed, the route is mounted, the handler is subscribed,
  the port has an adapter bound in the composition root. A new adapter nobody binds is
  the most common never-wired shape in a hexagonal layout.
- **Schema**: was the migration applied in that environment, in that order?
- **Runtime**: is the worker process, the consumer, the cron actually up? A queue with no
  consumer accepts messages forever and looks healthy.
- **Build**: is it in the shipped bundle at all? Tree-shaking, `--production` filtering,
  and per-environment builds can drop it.

## Deletion is proposed, not performed

When the verdict is Dead, this audit proposes deletion with the evidence (searches run,
config repositories and generated registries checked, consumers asked); the deletion is a
separate authorized change. Without that evidence the verdict is `unproven`, and it stays.

## Tools are hypothesis generators

| Source | Use as |
|---|---|
| `vulture`, `knip`/`ts-prune`, `deadcode`/staticcheck `U1000`, Rust `dead_code` lint | candidate list only (R1) |
| Coverage from an integration run (`go build -cover`, coverage of a real workload) | R4 evidence |
| Production coverage, sampled traces, a counter with nonzero prod traffic | R5 evidence |
| Feature-flag service state for the prod audience | R3 evidence |
