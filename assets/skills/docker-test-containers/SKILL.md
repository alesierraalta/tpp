---
name: docker-test-containers
description: "Trigger: start a throwaway container for a test, spin up Postgres/Redis/Mongo for an integration run, docker compose for local testing, 'no space left on device', reclaiming Docker disk, containers or volumes piling up, WSL disk keeps growing. Ephemeral containers that cost zero disk, plus staged reclaim."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "1.1"
  scope: [global]
  auto_invoke: "Before starting any container used for testing, and whenever Docker disk usage is the problem"
---

## Activation Contract

Load before starting **any** container for tests, evals, or local experiments, and whenever
Docker disk usage is the complaint.

This skill is harness-neutral: Claude Code, opencode, and pi all read it from the same file.

## Cost Model — read this before deciding what to delete

Keeping containers around costs different things per object type; getting it wrong is why disks fill up.

| Object | What keeping it actually costs | Verdict |
|---|---|---|
| **Image** | Content-addressed layers, shared. Twenty containers off one image cost **one** image. | Keep a small pinned set. Cheap. |
| **Stopped container** | Only its writable layer, usually a few MB of disk. | Cheap in bytes, expensive in clutter: see below. |
| **Named volume** | A full copy of the data directory. Survives `docker rm` **and** `docker compose down`. | Never keep one for a test. |
| **Anonymous volume** | Same full copy, with no name to notice it by. One created per run. | This is the leak. |
| **Build cache** | Grows unbounded until a GC policy caps it. | Cap it in `daemon.json`. |

Volumes and distinct image tags are what cost disk, not containers. Containers still accumulate in
ways bytes do not measure:

- **Name and port collisions.** `docker run --name test-pg` fails once a dead `test-pg` exists, and
  a leftover fixed port steals it from the next run.
- **Resurrection.** A container with a restart policy comes back on every daemon start, holds its
  port, and keeps its volumes referenced, so `docker volume prune` cannot reclaim them.
- **Unfindability.** Without a label nothing marks which containers were throwaway, so the only
  safe sweep is none.

The fix is label discipline plus `--rm`. Prefer a mechanism that survives forgetting it: label
everything, sweep by label.

## Hard Rules

- If the project already uses Testcontainers (any language binding), use it: its Ryuk reaper
  removes containers, networks and volumes even after a `kill -9`. Container reuse is
  experimental and must not be enabled in CI. The rules below cover the raw `docker` / compose path.
- Every test `docker run` carries **both** `--rm` and `--label ephemeral=1`. `--rm` removes the
  container and its anonymous volumes on exit; the label is the safety net for when `--rm` did not
  fire (daemon restart, `kill -9`, reboot). Assume `--rm` will be forgotten and keep the sweep possible.
- Compose gets the same label once per service in the YAML: `labels: { ephemeral: "1" }`.
- `docker compose run` leaves the container behind. It is **`docker compose run --rm`**, always.
  `docker compose up` does not need it because `down -v` handles teardown.
- **Never give a test container a restart policy.** No `--restart`, no `restart:` in the YAML.
  Find offenders with the sweep section below.
- `docker rm <c>` does **not** remove the anonymous volume. Only `docker rm -v <c>` does.
- `docker compose down` does **not** remove named volumes. Always
  `docker compose -p <owned-test-project> down -v --remove-orphans` (after preview and explicit confirmation; retain state when an inspection or rollback exception applies).
- Put test state on `--tmpfs`, never on a volume. It lives in RAM, costs zero disk, and dies with
  the container. The mount path depends on the image version: see the Postgres recipe.
- Never build a new image tag per test run. Reuse one tag, or use the official image plus mounted
  init scripts. A fresh tag per run leaves the previous one dangling.
- Never run bare `docker system prune -a` to fix disk pressure. It deletes images that stopped work
  still needs and the entire build cache. For disk pressure use the `wsl-disk-reclaim` skill.
- Publish ports on loopback only, `-p 127.0.0.1:55432:5432`, or `-p 127.0.0.1:0:5432` and read the
  port back. The password is `test`: never expose the service on all interfaces. Never bind the
  default port: a real local service or a parallel test will steal it.
- Optionally bound resources with `--memory 1g --cpus 2` so a runaway test cannot starve the host.
- Clean up in a `trap`, not at the end of the script. An interrupted run must not leak.
- Disk reclaim, WSL2 VHDX compaction and build-cache policy are out of scope here: use `wsl-disk-reclaim`.

## Decision Gates

| Situation | Do this |
|---|---|
| Project already uses Testcontainers | Use it (Ryuk reaper); keep reuse off in CI. |
| Need a DB for a test run | tmpfs recipe + `--rm`. Zero disk, zero cleanup. |
| Need >1 service wired together | Compose with a unique `-p` project and a `trap ... down -v`. |
| Need to inspect state after a failure | Run **without** `--rm`, inspect, then `docker rm -v <c>`. Never leave it. |
| Dataset too large for RAM | Named volume with an explicit name you can find, plus teardown in the same script. |
| Same service across many projects | One pinned tag everywhere. Ten projects, one image. |
| "No space left on device" | Use the `wsl-disk-reclaim` skill; sweep leaked test state only through the scoped sweep below. |

## Recipes

### Postgres + pgvector, zero disk

```bash
RUN_ID="pg-$$"
docker run --rm -d --name test-pg --label ephemeral=1 --label "run=$RUN_ID" \
  --memory 1g --cpus 2 \
  -e POSTGRES_PASSWORD=test -e PGDATA=/pgdata \
  --tmpfs /pgdata:rw,size=512m,mode=1777 \
  -p 127.0.0.1:55432:5432 pgvector/pgvector:pg16
```

`PGDATA` points **outside** the image's declared `VOLUME`, so no anonymous volume is created (works
for PG16 and PG17 images). PG18+ images moved `PGDATA` and the `VOLUME` to `/var/lib/postgresql`
(data in a version subdirectory such as `/var/lib/postgresql/18/docker`): for those, either keep
`PGDATA=/pgdata` on tmpfs as above, or mount the tmpfs at `/var/lib/postgresql` itself. Mounting
only `/var/lib/postgresql/data` on PG18+ leaves the declared volume unmounted and creates an
anonymous volume. Check the result with `docker inspect -f '{{.Mounts}}' test-pg` (no `volume` entry).
Plain `postgres` has no `vector` extension; use the pgvector image when the code needs embeddings.

Wait on readiness over TCP, never on `sleep`. The image's init phase serves only the Unix socket,
so a socket check passes before the real server accepts connections:

```bash
for i in $(seq 1 30); do docker exec test-pg pg_isready -h 127.0.0.1 -p 5432 -U postgres >/dev/null 2>&1 && break; sleep 1; done
```

### Redis, no persistence

```bash
RUN_ID="redis-$$"
docker run --rm -d --name test-redis --label ephemeral=1 --label "run=$RUN_ID" -p 127.0.0.1:56379:6379 \
  redis:7-alpine redis-server --save "" --appendonly no
```

Disabling both snapshots and the AOF keeps Redis entirely out of the filesystem.

### Compose for a test run
```bash
P="test-$$"
trap 'docker compose -p "$P" down -v --remove-orphans' EXIT INT TERM
docker compose -p "$P" up -d --wait
# one-off command in a new container: --rm or it stays behind
docker compose -p "$P" run --rm app pytest
```

The unique `-p` lets parallel runs coexist and makes teardown exact. `--wait` blocks on each
service's healthcheck, so no polling loop is needed.

```yaml
services:
  db:
    image: pgvector/pgvector:pg16
    labels:
      ephemeral: "1"
      run: "${TEST_RUN_ID:?set TEST_RUN_ID}"
    environment:
      POSTGRES_PASSWORD: test
      PGDATA: /pgdata
    tmpfs:
      - /pgdata:size=512m,mode=1777
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -h 127.0.0.1 -p 5432 -U postgres"]
      interval: 1s
      retries: 30
```

### Helper scripts

- `assets/ephemeral-pg.sh` — starts a tmpfs Postgres on a free loopback port, waits for TCP readiness, prints
  the DSN, and removes it on exit.
- `assets/sweep-ephemeral.sh` — previews exact container/volume/network IDs, then removes only an explicitly confirmed ownership scope (`--apply --scope-label run=... --confirm`); retains and reports ambiguous/unowned items. Dry-run by default. Listing or inspect failures are errors, never empty results.
- `assets/docker-reclaim.sh` — report-first staged reclaim helper, dry-run by default; apply delegates one sweep execution. Disk-pressure guidance itself lives in `wsl-disk-reclaim`.

## Sweep — the safety net for owned, labelled state

`--rm` fails open: if the daemon restarts, the host reboots, or the process is `kill -9`'d, the
container survives. The label makes survivors findable, but ownership and the test-run target still must be confirmed. A bounded preview identifies candidates without implying that every labelled object is disposable:

```bash
./assets/sweep-ephemeral.sh --scope-label run=ci-123       # preview exact IDs
# After checking the preview, confirm this bounded scope explicitly:
./assets/sweep-ephemeral.sh --apply --scope-label run=ci-123 --confirm
```

The label is necessary but not sufficient: identify the owning test run/project and preview the exact target set first. The scope value must include a unique run/project ID (for example, `run=ci-123`); a scope equal only to `ephemeral=1`, or reused by multiple projects, cannot distinguish ownership and is unsafe. Require explicit confirmation before destructive deletion, keep scope bounded, and never treat all labelled state as disposable when ownership is uncertain. Report skipped ambiguous or unowned items. Unlabelled project services remain out of reach.

**`-v` is not optional, and the label alone will not find the volumes.** Docker does **not**
propagate a container's labels to its anonymous volumes, so
`docker volume ls --filter label=ephemeral=1` comes back empty even when those volumes exist. The
`-v` on `docker rm` is the only thing that reclaims them.

That is also why the sweep must run **before** `docker volume prune`: a volume still attached to a
surviving container is "in use" and prune skips it. Containers first, volumes second.

Volumes and networks you create *explicitly* should carry both labels so they are findable within one bounded run:

```bash
docker volume create --label ephemeral=1 --label run=ci-123 test-data
docker network create --label ephemeral=1 --label run=ci-123 test-net
./assets/sweep-ephemeral.sh --scope-label run=ci-123
./assets/sweep-ephemeral.sh --apply --scope-label run=ci-123 --confirm
```

Run the ownership-filtered preview at the **start** of a test script, not only the end. The
`ephemeral=1` label alone never authorizes deletion; an ambiguous or unowned object is reported. Unlabeled or ambiguous named volumes are retained, while explicitly scope-labeled named volumes are owned by that scope and removed.
and retained.

### Finding what resurrects

```bash
docker ps -a --format '{{.Names}}' | while read n; do
  p=$(docker inspect -f '{{.HostConfig.RestartPolicy.Name}}' "$n")
  [ "$p" != "no" ] && echo "$p  $n"
done
```

Anything listed here comes back on its own. Before manually retiring one, identify its owner,
obtain explicit authorization and confirmation, and verify the intended retention decision. Only
then may an authorized operator disable its restart policy and remove it; never treat
`docker update --restart=no <name> && docker rm -v <name>` as an unguarded action.

`assets/sweep-ephemeral.sh` previews all ephemeral candidates but deletes only the explicitly
owned scope, dry-run by default. `assets/docker-reclaim.sh` preserves the report-first stages and
routes any destructive cleanup through that scoped sweep.

## Output Contract

When this skill is applied, report:

- the exact `docker run` / compose invocation used (or the Testcontainers setup), and that it carries `--rm`, a `trap` teardown, or the Ryuk reaper;
- the port bound (loopback) and how readiness was confirmed;
- for a sweep: the previewed IDs, the scope label, and skipped ambiguities.

Never run destructive deletion without explicit confirmation; orphaned data is still data until ownership and retention are established.

## References

- `assets/ephemeral-pg.sh`, `assets/sweep-ephemeral.sh`, `assets/docker-reclaim.sh` — the helpers.
- `wsl-disk-reclaim` skill — disk reclaim, WSL2 VHDX compaction, build-cache policy.
- Docker docs: `docker volume prune`, compose `tmpfs` and `--wait`; Testcontainers docs for the Ryuk reaper.
