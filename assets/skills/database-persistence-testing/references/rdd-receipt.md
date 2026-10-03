# RDD evidence receipt (`lens:persistence`)

When running under RDD or when `--receipt` is requested, emit a JSON evidence receipt/artifact conforming to `_shared/test-receipt-contract.md`:

- `lens`: `"persistence"`
- `skill`: `"database-persistence-testing"`
- `negative_control`: Record verified falsifiability evidence appropriate to the selected risk (e.g. baseline migration failure or query budget breach before optimization). Without required evidence, verdict is `REJECTED`.
- Generated via: `python3 ~/.claude/skills/_shared/generate-test-receipt.py --lens persistence --skill database-persistence-testing --out <path>`; the caller creates the parent directory and chooses `<path>`.
- Path convention: `.atl/receipts/<provider_lineage_id_or_unbound>/persistence.json`; `unbound` is a storage label only, never a native lineage.
- It records evidence, telemetry, and storage metadata only; it is not a cryptographic signature, software-test approval, native review authority, consent, provider lineage, acknowledgement, or delivery approval. Native review authority comes exclusively from the provider-issued `gentle_review` lifecycle.

Derivation rule: a receipt is generated FROM an evidence ledger row (`~/.claude/skills/test-strategy/references/evidence.md`), never written independently: same command, inputs, observed output digest, and negative control. Emit it only when a provider-issued lineage id is available for the candidate; without lineage the ledger row is the record and no `unbound` receipt is written.
