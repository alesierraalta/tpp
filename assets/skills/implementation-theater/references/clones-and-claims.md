# Two implementations, and code that lies about itself

## Divergent clones — duplication is not the finding, DIVERGENCE is

Two copies exist, a bug is found, and the fix lands in one of them. This skill owns clone
detection (handed over from `clean-architecture-audit`); it is a finding only with a
violated shared contract or demonstrated behavioral divergence plus reachability of the
diverging copy.

Procedure:

1. **Find the twin.** CodeGraph or a similarity search on the function body, plus a grep
   for a distinctive literal, error message, magic number or SQL fragment from the code
   under audit. Look across packages and repos, not just the directory.
2. **Establish which one RUNS** — the R chain in `reachability.md`. Often both do, for
   different callers, which is the worst case.
3. **Diff them and read the difference.** Every divergence is either an intentional
   variation (say why) or a behavioral difference that violates the shared contract.
4. **Check the history**: `git log -S "<distinctive fragment>"` over both paths. A commit
   touching one and not the other is corroboration to investigate, not proof.
5. Decide: consolidate, or record the divergence with the evidence and the owner of each
   copy. Undocumented twins guarantee the next fix diverges again.

Same procedure for a partial clone: the same logic reimplemented with different names
(the arithmetic repeated inside a test instead of calling the unit, a threshold applied
in two places, a mapping duplicated in the frontend and the backend).

## Claim falsification — the name is a contract

Names, docstrings, comments, READMEs and PR descriptions are ASSERTIONS. Code-comment
inconsistency is a well-studied and common defect class; the fix is mechanical.

For each claim: write it as one testable sentence, then find the LINE that makes it true.
No such line → the claim is false. A false name is worse than a missing comment, because
everyone downstream programs against the name.

High-yield claim shapes to check:

- `validate_` / `check_` / `verify_` — does it ever reject anything?
- `cached_` / `memoized_` — is there a hit path, and does it ever hit?
- `_safe` / `_secure` / `sanitize_` — what exactly does it make safe, and against what?
- `async` / `parallel` / `batch` — does it actually not block, actually run concurrently,
  actually send one request instead of N?
- `atomic` / `transactional` — is there a transaction, and what is inside it?
- `retry` / `backoff` — how many attempts, with what delay, and what happens at the end?
- `all_` / `bulk_` / `_many` — one call, or a loop pretending to be one?
- A docstring stating a return type, a raised exception, a unit (ms vs s), a bound
  ("never more than N") or an ordering guarantee — each is directly testable.
- A README or PR line claiming a number, a benefit or a behavior — ask for its
  measurement; an unmeasured claim in a PR description is the same defect at a
  larger scale.

Confident comments are the best hunting ground: a comment explaining WHY something is
safe is a hypothesis someone once had. Falsify it against the current code, not against
the code as it was when the comment was written.

## Output shape

State each finding as: the claim, the line that should make it true, and what the code
does instead. Then the fix — usually renaming or deleting the claim is correct and
cheaper than making the code live up to it. Say which you chose, with confidence, unknowns
and the next proof.
