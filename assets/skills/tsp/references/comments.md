# Comments that stay true

A comment is read by the next person or model as a fact about the code next to it. A comment
that is wrong is acted on as if it were right. So a comment is held to the same standard as an
assertion: true, short, and placed where it applies.

## Rules

1. **One line of intent or a non-obvious why**, on the line or block it explains. If the code
   says what, the comment says why, or nothing.
2. **Never restate the code.** `// increment counter` above `counter++` is deleted on sight.
3. **Never narrate.** No history ("changed from X to Y"), no session ("now we add…"), no plan
   ("TODO later", "will refactor"). History lives in git; plans live in the test plan or issues.
4. **No block headers for obvious sections.** `// ---- helpers ----` adds nothing a reader
   cannot see. Keep headers only where a file has genuinely distinct concerns.
5. **A stale comment is a defect** of the same weight as a wrong assertion. When the code
   under a comment changes, the comment is fixed or deleted in the same change, never left.
6. **Checked at promotion and at review.** Every comment in the touched lines is compared
   with the code it annotates. Promoted tests carry only: one-line intent where needed, the
   characterization label, and the evidence ledger id.

## Examples

Good — one-line why that the code cannot say:

```ts
// D1 reports the partial unique index as a generic constraint error; match by index name.
if (isDuplicatePgaError(err)) return conflict("PGA_NUMBER_TAKEN");
```

Restating — delete:

```ts
// check if the token is valid
if (!isValidToken(token)) return denied();
```

Stale — the code no longer matches; fix or delete in the same change:

```ts
// returns null when the member has no credential
const credential = await findActiveCredential(db, memberId); // now throws NotFound
```

Correct form after the fix:

```ts
// Throws NotFound so callers cannot treat "no credential" as a valid empty state.
const credential = await findActiveCredential(db, memberId);
```
