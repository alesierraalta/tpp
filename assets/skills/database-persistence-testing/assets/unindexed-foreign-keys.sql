-- Audit for foreign keys without a valid supporting index in PostgreSQL.
-- A supporting index is valid, non-partial, non-expression, and has the FK columns
-- as its LEADING key columns (any order). int2vector subscripts are 0-based, so the
-- leading n columns are [0:n-1]. Composite-PK and unique indexes count as support.
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
