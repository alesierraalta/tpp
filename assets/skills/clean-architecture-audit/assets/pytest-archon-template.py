"""Architecture conformance template (pytest).

Adapt PACKAGE / SRC_ROOT to the target. Every rule below ships with a negative control:
a rule that has never gone red proves nothing (it may match zero modules).
"""
import ast
from pathlib import Path

from pytest_archon import archrule

PACKAGE = "app"
SRC_ROOT = Path("src")  # directory that contains the PACKAGE directory


def test_clean_architecture_domain_purity():
    """Domain core must not depend on infrastructure, frameworks, or database ORMs."""
    (
        archrule("domain_must_be_pure")
        .match("app.domain.*")
        .should_not_import(
            "app.infrastructure.*",
            "app.adapters.*",
            "app.entrypoints.*",
            "fastapi.*",
            "sqlalchemy.*",
            "httpx.*",
        )
        .check(PACKAGE)
    )


def test_application_depends_only_on_domain():
    """Application use cases depend on domain abstractions, not web or DB internals."""
    (
        archrule("application_dependency_inversion")
        .match("app.application.*")
        .should_not_import(
            "app.infrastructure.*",
            "app.entrypoints.*",
            "fastapi.*",
        )
        .check(PACKAGE)
    )


# --- Cycle detection: domain -> domain imports stay allowed; only cycles fail. ---

def import_graph(src_root: Path, package: str) -> dict[str, set[str]]:
    """Module -> set of in-package modules it imports (absolute and relative imports)."""
    modules = {
        ".".join(p.relative_to(src_root).with_suffix("").parts).removesuffix(".__init__"): p
        for p in (src_root / package).rglob("*.py")
    }
    graph: dict[str, set[str]] = {name: set() for name in modules}
    for name, path in modules.items():
        # The package a relative import is resolved against: the module itself for an __init__.
        here = name if path.name == "__init__.py" else name.rpartition(".")[0]
        for node in ast.walk(ast.parse(path.read_text())):
            targets = []
            if isinstance(node, ast.Import):
                targets = [a.name for a in node.names]
            elif isinstance(node, ast.ImportFrom):
                base = node.module or ""
                if node.level:
                    parts = here.split(".")
                    anchor = ".".join(parts[: len(parts) - (node.level - 1)])
                    base = f"{anchor}.{base}" if base else anchor
                targets = [base] + [f"{base}.{a.name}" for a in node.names]
            graph[name] |= {t for t in targets if t in modules and t != name}
    return graph


def find_cycles(graph: dict[str, set[str]]) -> list[list[str]]:
    """Strongly connected components of size > 1 (Tarjan)."""
    index: dict[str, int] = {}
    low: dict[str, int] = {}
    stack: list[str] = []
    on_stack: set[str] = set()
    out: list[list[str]] = []
    counter = [0]

    def visit(v: str) -> None:
        index[v] = low[v] = counter[0]
        counter[0] += 1
        stack.append(v)
        on_stack.add(v)
        for w in graph[v]:
            if w not in index:
                visit(w)
                low[v] = min(low[v], low[w])
            elif w in on_stack:
                low[v] = min(low[v], index[w])
        if low[v] == index[v]:
            comp = []
            while True:
                w = stack.pop()
                on_stack.discard(w)
                comp.append(w)
                if w == v:
                    break
            if len(comp) > 1:
                out.append(sorted(comp))

    for v in graph:
        if v not in index:
            visit(v)
    return out


def test_no_circular_dependencies():
    """Fails on any import cycle in the package, including inside the domain."""
    graph = import_graph(SRC_ROOT, PACKAGE)
    # An empty graph means SRC_ROOT/PACKAGE is wrong: a pass there would prove nothing.
    assert graph, f"no modules found under {SRC_ROOT / PACKAGE}"
    assert find_cycles(graph) == []


# --- Negative controls: prove the cycle check can fail (and does not over-fail). ---

def _make_pkg(root: Path, files: dict[str, str]) -> None:
    for rel, body in files.items():
        path = root / "app" / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(body)


def test_cycle_check_goes_red_on_a_real_cycle(tmp_path):
    _make_pkg(tmp_path, {"__init__.py": "", "a.py": "import app.b\n", "b.py": "import app.a\n"})
    assert find_cycles(import_graph(tmp_path, "app")) == [["app.a", "app.b"]]


def test_cycle_check_goes_red_on_a_relative_import_cycle(tmp_path):
    _make_pkg(tmp_path, {"__init__.py": "", "a.py": "from . import b\n", "b.py": "from .a import X\n"})
    assert find_cycles(import_graph(tmp_path, "app")) == [["app.a", "app.b"]]


def test_cycle_check_stays_green_on_one_way_domain_imports(tmp_path):
    _make_pkg(tmp_path, {"__init__.py": "", "a.py": "import app.b\n", "b.py": "X = 1\n"})
    assert find_cycles(import_graph(tmp_path, "app")) == []
