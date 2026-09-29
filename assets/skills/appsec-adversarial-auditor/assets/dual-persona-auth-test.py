"""Dual-persona authorization harness. Fill every CONFIG value for the target under test.

A denial only counts when a positive control proves the same request works for the owner,
so a wrong route or base URL (404 for everyone) cannot pass as "denied". Run only against
an isolated target with synthetic identities and data.
"""
import os

import pytest
import requests

# --- CONFIG (operator fills) -------------------------------------------------
BASE_URL = os.environ.get("TARGET_BASE_URL", "http://localhost:8000/api/v1")
TOKEN_OWNER = os.environ.get("TOKEN_OWNER", "")        # synthetic owner (tenant A)
TOKEN_ATTACKER = os.environ.get("TOKEN_ATTACKER", "")  # synthetic peer (tenant B)
TOKEN_MEMBER = os.environ.get("TOKEN_MEMBER", "")      # low-privilege user of tenant A
RES = "/documents"                                     # resource collection route
ADMIN_ROUTE = "/admin/export"                          # privileged function, member must be denied
PROTECTED_FIELDS = {"is_admin": True, "tenant_id": "tenant_b", "owner_id": "attacker"}
DENIED = (401, 403, 404)                               # the declared denial contract
TIMEOUT = 10
# ------------------------------------------------------------------------------


def _h(token):
    return {"Authorization": f"Bearer {token}"}


def call(method, path, token, **kw):
    return requests.request(method, f"{BASE_URL}{path}", headers=_h(token), timeout=TIMEOUT, **kw)


@pytest.fixture(scope="module", autouse=True)
def _require_tokens():
    if not (TOKEN_OWNER and TOKEN_ATTACKER):
        pytest.fail("TOKEN_OWNER and TOKEN_ATTACKER must be set: an unconfigured run proves nothing")


@pytest.fixture
def doc():
    r = call("POST", RES, TOKEN_OWNER, json={"title": "Confidential Roadmaps", "content": "Top Secret"})
    assert r.status_code in (200, 201), f"fixture setup failed: {r.status_code} {r.text[:200]}"
    body = r.json()
    yield body
    call("DELETE", f"{RES}/{body['id']}", TOKEN_OWNER)


def test_bola_cross_tenant_isolation(doc):
    path = f"{RES}/{doc['id']}"

    # Positive control: the owner reads the same route and gets the real body.
    own = call("GET", path, TOKEN_OWNER)
    assert own.status_code == 200 and own.json().get("title") == "Confidential Roadmaps", (
        "positive control failed: route or base URL is wrong, denial results below would be meaningless"
    )
    before = own.json()

    # Read as attacker.
    r = call("GET", path, TOKEN_ATTACKER)
    assert r.status_code in DENIED, f"BOLA read: attacker got {r.status_code} for {doc['id']}"
    assert "Top Secret" not in r.text, "BOLA read: denial status but the body leaks the resource"

    # Mutate as attacker, then prove the state did not change (a 403 can still write).
    r = call("PUT", path, TOKEN_ATTACKER, json={"title": "Hacked Title"})
    assert r.status_code in DENIED, f"BOLA write: attacker got {r.status_code}"
    assert call("GET", path, TOKEN_OWNER).json() == before, "denied mutation still changed state"

    # Delete as attacker, then prove the resource still exists.
    r = call("DELETE", path, TOKEN_ATTACKER)
    assert r.status_code in DENIED, f"BOLA delete: attacker got {r.status_code}"
    assert call("GET", path, TOKEN_OWNER).status_code == 200, "denied delete still removed the resource"

    # Positive control for mutation: the owner can perform the same PUT.
    r = call("PUT", path, TOKEN_OWNER, json={"title": "Owner Edit"})
    assert r.status_code in (200, 204), f"owner control for PUT failed: {r.status_code}"


def test_bfla_member_cannot_call_admin_function():
    if not TOKEN_MEMBER:
        pytest.fail("TOKEN_MEMBER must be set for the vertical (BFLA) test")
    admin = os.environ.get("TOKEN_ADMIN", TOKEN_OWNER)
    # Positive control: an authorized role reaches the function.
    assert call("GET", ADMIN_ROUTE, admin).status_code == 200, "admin control failed: wrong ADMIN_ROUTE?"
    r = call("GET", ADMIN_ROUTE, TOKEN_MEMBER)
    assert r.status_code in DENIED, f"BFLA: member reached {ADMIN_ROUTE} with {r.status_code}"


def test_mass_assignment_protected_fields_ignored_or_rejected(doc):
    path = f"{RES}/{doc['id']}"
    before = call("GET", path, TOKEN_OWNER).json()
    r = call("PUT", path, TOKEN_OWNER, json={"title": "Renamed", **PROTECTED_FIELDS})
    assert r.status_code in (200, 204, 400, 403, 422), f"unexpected status {r.status_code}"
    after = call("GET", path, TOKEN_OWNER).json()
    for field, injected in PROTECTED_FIELDS.items():
        # Bare field name only: the response schema decides whether it is visible.
        if field in after:
            assert after[field] == before.get(field), f"mass assignment: {field} changed to {after[field]!r}"
            assert after[field] != injected
