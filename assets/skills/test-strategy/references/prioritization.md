# Building the ranked list from data you already have

Stop guessing what deserves a test. Four signals, all computable in minutes, produce an
ordered list. Output the list BEFORE writing anything.

## Step 0 — Inventory: know what exists before ranking

Ranking only what you happen to see leaves the rest invisible. Before the four inputs,
enumerate the entry points with CodeGraph (`codegraph_explore`, or the read-only CLI:
`codegraph files`, `codegraph query`, `codegraph callers`): HTTP routes, CLI commands,
scheduled jobs and queue consumers, migrations, ports and their adapters, and the public
library API. Add the two or three critical user journeys the business cannot lose.

A target missing from the inventory cannot be ranked, and therefore is a silent gap: it
never reaches the plan, never gets a status, and nobody notices. Record the inventory in
the plan before ranking anything.

## The four inputs

**1. Blast radius — who breaks if this is wrong.** CodeGraph already emits this per
symbol, including the `no covering tests found` warning. That pairing — many callers and
nothing pinning it — is a strong target, and it is handed to you for free. Prefer the transitive count over the direct one; a port with three adapters
behind it carries all of them.

**2. Historical defect density — a strong signal in many repositories; verify it locally.** Code
that has been fixed before tends to be fixed again.

```bash
git log --oneline --since=2.years -- <path> | wc -l                 # churn
git log --oneline --since=2.years --grep='^fix' -- <path> | wc -l   # past defects
git log --format='%an' --since=2.years -- <path> | sort -u | wc -l  # authors (weak hint: squash merges and bots distort it)
```

High churn plus high fix count plus many authors is where the money is. Stable code
untouched for two years, whatever its coverage, is not your problem today.

**3. Consequence class — what it costs when it is wrong.** Rank explicitly:
money moved or lost · data destroyed or corrupted · a permission or tenant boundary ·
a silently wrong answer a human will act on · a visible error · cosmetic. A silently
wrong answer outranks a visible crash: the crash announces itself, the wrong answer does
not (`silent-degradation`).

Error-handling code ranks high by default. In five distributed data systems, Yuan et al.
(OSDI 2014) found 92% of catastrophic failures came from mishandled non-fatal errors, about one
third were trivial handler mistakes (empty, abort or TODO handlers), and 77% were reproducible by a
unit test. A `catch` with nothing in it, a handler that returns success, or a `TODO` inside one is
a probe target before any happy path is.

**4. Existing evidence and bounded cost.** Record the evidence type, result, limitations,
and cost before choosing the next probe. For mutation, preserve Stryker's raw outcomes:
killed, survived, no coverage, timeout, and invalid; Stryker treats a timeout as detected,
not killed. Separately label equivalent survivors and any custom metric or interpretation;
do not silently redefine MSI. Use mutation only for top candidates when it is meaningful,
within a bounded budget.

## Combining them

Do not build a formula or pretend the signals are precise scores. Sort qualitatively using
consequence, likelihood/history, existing evidence, and cost; defend each placement in one line:

| Bucket | Profile | Action |
|---|---|---|
| **Probe** | High consequence or credible likelihood/history, weak evidence, affordable probe | Route to the assigned sibling (usually `exploit-testing`); it climbs its full ladder to the target rung |
| **Pin** | Meaningful behavior with a focused gap and proportionate cost | Contract test at the surviving boundary, executed via the assigned sibling skill |
| **None** | Low consequence/likelihood, adequate evidence, or disproportionate cost | Record the reason; keep the row |

Budget never removes a row. It orders rows and sets how far execution gets today; the
remainder stays in the plan with status pending.

## Anti-priorities — name them, do not test them

Getters, setters, dataclasses and value objects with no logic · one-line passthroughs ·
pure config mapping · code the type checker already guarantees · framework behavior (you
are testing the framework, not your code) · generated code · code scheduled for deletion ·
**code with no callers at all** — first triage dynamic/public entry reachability (routes,
reflection, plugins, jobs, or external consumers). Exclude it only when that check finds no
reachable contract; otherwise rank the reachable behavior normally.

Writing these down matters as much as the list itself: a reviewer who sees "not testing
X because Y" stops asking, and a silent gap looks identical to an oversight.

## The coverage trap

A coverage percentage counts lines executed. A line executed by a test with no meaningful
assertion counts exactly the same as one properly pinned. That is why coverage targets
reliably produce tests that assert nothing — the metric is satisfied by execution alone.
Measured, not opined: across five large Java systems, coverage correlated only
weakly with effectiveness once suite size was controlled (Inozemtseva & Holmes, ICSE 2014).

When someone asks for a coverage number, translate the request: they want confidence that
a regression would be caught. Answer with the mutation result on the ranked list, which
is the same question asked honestly. Report coverage, if you must, as context — never as
the goal.
