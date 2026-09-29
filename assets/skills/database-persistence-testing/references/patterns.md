# Database & Persistence Testing Reference Patterns

## 0. Safe Reversible Migration Check

`assets/migration-idempotency-check.sh` is limited to explicitly declared reversible migrations. Invoke it with a nonempty `--target` label and three nonempty executable-plus-argument argv arrays separated by `--`:

```sh
./assets/migration-idempotency-check.sh --mode reversible --target users-v3 -- \
  alembic upgrade head -- alembic downgrade -1 -- pg_dump --schema-only --dbname test_database
```

The caller must complete authorization and preflight; the label is not proof of authorization. The script uses a private temporary workspace and proves only canonical schema-dump equality: baseline vs rollback and first UP vs re-applied UP. It does not prove data preservation, semantic dump normalization, or application compatibility. Expand/contract or irreversible migrations must use the compatibility and documented rollback or backup/restore path instead; this check never runs DOWN for them.

## 1. Unindexed Foreign Key Detection Query (PostgreSQL)

Without an index on the referencing columns, a parent `DELETE`/`UPDATE` of a referenced key takes `RowShareLock` on the child table and runs a sequential scan of the child per parent row (`SHARE ROW EXCLUSIVE` is the lock of `ADD FOREIGN KEY`, not of this path). Prioritize by child-table size and parent delete/update frequency. The query is `assets/unindexed-foreign-keys.sql` (int2vector subscripts are 0-based; the FK columns must be the leading columns of a valid, non-partial, non-expression index):

```sql
SELECT
    c.conrelid::regclass AS table_name,
    c.conname AS foreign_key_name,
    pg_get_constraintdef(c.oid) AS constraint_definition
FROM pg_constraint c
WHERE c.contype = 'f'
  AND NOT EXISTS (
      SELECT 1
      FROM pg_index i
      WHERE i.indrelid = c.conrelid
        AND i.indisvalid
        AND i.indpred IS NULL
        AND i.indexprs IS NULL
        AND i.indnkeyatts >= array_length(c.conkey, 1)
        AND (i.indkey::int2[])[0:array_length(c.conkey, 1) - 1] @> c.conkey
        AND (i.indkey::int2[])[0:array_length(c.conkey, 1) - 1] <@ c.conkey
  )
ORDER BY table_name, foreign_key_name;
```

## 2. Zero-Downtime Expand/Contract Migration Protocol

### Phase 1: Expand
Add new column as nullable, or with a non-volatile default (Postgres 11+ stores it as metadata; volatile defaults, identity and stored generated columns rewrite the table):
```sql
-- Migration 20260904_expand_user_full_name.sql
SET lock_timeout = '2s';
ALTER TABLE users ADD COLUMN full_name VARCHAR(255);
```
Code writes to BOTH `first_name`/`last_name` AND `full_name`. Code reads from `first_name`/`last_name`.

### Phase 2: Backfill (Chunked Background Migration)
```python
# background_backfill.py
BATCH_SIZE = 1000
cursor = 0

while True:
    rows_updated = db.execute("""
        WITH target AS (
            SELECT id FROM users
            WHERE id > :cursor AND full_name IS NULL
            ORDER BY id ASC LIMIT :batch_size
        )
        UPDATE users u
        SET full_name = CONCAT(u.first_name, ' ', u.last_name)
        FROM target
        WHERE u.id = target.id
        RETURNING u.id;
    """, {"cursor": cursor, "batch_size": BATCH_SIZE})
    
    if not rows_updated:
        break
    cursor = max(r[0] for r in rows_updated)
    time.sleep(0.05) # Throttle to prevent replication lag and WAL saturation
```

### Phase 3: Contract
After all rows are backfilled and code reads exclusively from `full_name`. Enforce NOT NULL without a long `ACCESS EXCLUSIVE` scan:
```sql
-- Migration 20260905_contract_full_name_not_null.sql
SET lock_timeout = '2s';
ALTER TABLE users ADD CONSTRAINT full_name_nn CHECK (full_name IS NOT NULL) NOT VALID;
-- next statement in its own transaction: SHARE UPDATE EXCLUSIVE, writes continue
ALTER TABLE users VALIDATE CONSTRAINT full_name_nn;
-- PG12+ skips the table scan because the validated CHECK proves no NULLs
ALTER TABLE users ALTER COLUMN full_name SET NOT NULL;
ALTER TABLE users DROP CONSTRAINT full_name_nn;
```
Drop the legacy columns in a later migration, after no deployed application version reads them (`ALTER TABLE users DROP COLUMN first_name, DROP COLUMN last_name;`).

## 3. Concurrency Lock Assertion (Pessimistic Queue Pattern)

The test must be able to fail: it seeds a known queue, forces the workers to overlap with a barrier, and asserts the union equals the seeded set with no duplicates and both workers non-empty. With plain `FOR UPDATE` the second worker blocks behind the first (the barrier or the `lock_timeout` trips and the test goes red); with `SKIP LOCKED` it takes disjoint rows.

```python
import threading
import pytest
from sqlalchemy import text

N = 20

def test_concurrent_worker_skip_locked(db_engine):
    with db_engine.begin() as conn:
        conn.execute(text("DELETE FROM task_queue"))
        conn.execute(text("INSERT INTO task_queue (id, status) SELECT g, 'pending' FROM generate_series(1, :n) g"), {"n": N})

    barrier = threading.Barrier(2, timeout=10)
    results = {}

    def worker(name):
        with db_engine.connect() as conn, conn.begin():
            conn.execute(text("SET LOCAL lock_timeout = '3s'"))
            rows = conn.execute(text(
                "SELECT id FROM task_queue WHERE status = 'pending' "
                "ORDER BY id FOR UPDATE SKIP LOCKED LIMIT :k"), {"k": N // 2}).fetchall()
            results[name] = {r[0] for r in rows}
            barrier.wait()  # both transactions hold their locks at the same time
            conn.execute(text("UPDATE task_queue SET status = 'completed' WHERE id = ANY(:ids)"),
                         {"ids": list(results[name])})

    threads = [threading.Thread(target=worker, args=(n,)) for n in ("a", "b")]
    for t in threads: t.start()
    for t in threads: t.join()

    a, b = results["a"], results["b"]
    assert a and b, "a worker processed nothing"
    assert a.isdisjoint(b), "the same item was claimed twice"
    assert a | b == set(range(1, N + 1)), "queued items were lost or left unprocessed"
```

Mutation check: replace `SKIP LOCKED` with nothing; worker `b` blocks on `a`'s row locks, never reaches the barrier in time, and the test fails with a timeout instead of passing.

## 4. Isolation anomalies (PostgreSQL semantics)

Run two real sessions with explicit barriers between steps. Behavior differs by engine (MySQL/InnoDB repeatable read is not PostgreSQL's); state the engine.

| Anomaly | Read Committed | Repeatable Read | Serializable | Other guard |
|---|---|---|---|---|
| Lost update (read, then write back) | possible | prevented (update fails with 40001) | prevented (40001) | `SELECT ... FOR UPDATE`, `version` column, atomic `SET x = x + 1` |
| Write skew (two txns each read a shared invariant, write disjoint rows) | possible | possible | prevented (40001) | lock the rows that carry the invariant, or a constraint |
| Read skew / non-repeatable read | possible | prevented | prevented | none needed |

On SQLSTATE `40001` (serialization failure) or `40P01` (deadlock) the application must roll back and retry the whole transaction, bounded, with backoff and the same idempotency key; assert the retry succeeds and the effect is applied once.

## 5. Migration lint, online change and restore

- Default static lint: `squawk` on the migration files (unsafe lock, rewrite, missing `CONCURRENTLY`). If it is not installed, walk the manual lock checklist (`ACCESS EXCLUSIVE` statements, table rewrites, non-concurrent index builds, `lock_timeout` set). `eugene` is an alternative that also traces locks.
- Online schema change beyond plain expand/contract: `pgroll` (PostgreSQL, reversible, dual schema versions) or `gh-ost` (MySQL, replica-lag throttle).
- Backup evidence: a dump exiting 0 proves nothing. Restore it into a scratch instance, run the invariant queries (row counts, constraints, a known record), and record elapsed time against the declared RTO.
