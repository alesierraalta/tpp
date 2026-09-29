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

Run `assets/unindexed-foreign-keys.sql` as is; it is the single source of truth for the query, so it is not copied here.

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

The test must be able to fail: it seeds a known queue, makes both workers overlap on their first batch with a barrier, then each worker keeps claiming small batches with `FOR UPDATE SKIP LOCKED` until no rows remain. It asserts the union equals the seeded set, the sets are disjoint, and both workers claimed at least one row. With plain `FOR UPDATE` the second worker blocks behind the first worker's open transaction, never reaches the barrier, and the test goes red (barrier or `lock_timeout` trips). Workers may finish at different times, so no worker is assumed to take a fixed share.

```python
import threading
import pytest
from sqlalchemy import text

N = 20
BATCH = 2

def test_concurrent_worker_skip_locked(db_engine):
    with db_engine.begin() as conn:
        conn.execute(text("DELETE FROM task_queue"))
        conn.execute(text("INSERT INTO task_queue (id, status) SELECT g, 'pending' FROM generate_series(1, :n) g"), {"n": N})

    barrier = threading.Barrier(2, timeout=10)
    results = {"a": set(), "b": set()}
    errors = []

    def claim(conn):
        rows = conn.execute(text(
            "SELECT id FROM task_queue WHERE status = 'pending' "
            "ORDER BY id FOR UPDATE SKIP LOCKED LIMIT :k"), {"k": BATCH}).fetchall()
        ids = [r[0] for r in rows]
        if ids:
            conn.execute(text("UPDATE task_queue SET status = 'completed' WHERE id = ANY(:ids)"), {"ids": ids})
        return ids

    def worker(name):
        try:
            with db_engine.connect() as conn:
                with conn.begin():  # first batch: the transaction stays open across the barrier
                    conn.execute(text("SET LOCAL lock_timeout = '3s'"))
                    results[name].update(claim(conn))
                    barrier.wait()  # both workers hold their first-batch locks at the same time
                while True:  # claim loop: small committed batches until the queue is empty
                    with conn.begin():
                        ids = claim(conn)
                    if not ids:
                        break
                    results[name].update(ids)
        except Exception as exc:  # a blocked or broken worker must fail the test, not hang it
            errors.append(exc)
            barrier.abort()

    threads = [threading.Thread(target=worker, args=(n,), daemon=True) for n in ("a", "b")]
    for t in threads: t.start()
    for t in threads: t.join(timeout=30)

    assert not any(t.is_alive() for t in threads), "a worker hung: the test timed out"
    assert not errors, errors
    a, b = results["a"], results["b"]
    assert a and b, "a worker claimed nothing"
    assert a.isdisjoint(b), "the same item was claimed twice"
    assert a | b == set(range(1, N + 1)), "queued items were lost or left unprocessed"
```

Mutation check: remove `SKIP LOCKED`; worker `b` blocks on `a`'s open row locks, never reaches the barrier, and the test fails with a timeout or lock error instead of passing.

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
