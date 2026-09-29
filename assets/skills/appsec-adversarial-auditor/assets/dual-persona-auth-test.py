"""Dual-persona authorization harness. Fill every CONFIG value for the target under test.

A denial only counts when a positive control proves the same request works for the owner,
so a wrong route or base URL (404 for everyone) cannot pass as "denied". Run only against
an isolated target with synthetic identities and data.
"""
import os
import warnings

import pytest
import requests

# --- CONFIG (operator fills) -------------------------------------------------
BASE_URL = os.environ.get("TARGET_BASE_URL", "http://localhost:8000/api/v1")
TOKEN_OWNER = os.environ.get("TOKEN_OWNER", "")        # synthetic owner (tenant A)
TOKEN_ATTACKER = os.environ.get("TOKEN_ATTACKER", "")  # synthetic peer (tenant B)
TOKEN_MEMBER = os.environ.get("TOKEN_MEMBER", "")      # low-privilege user of tenant A
TOKEN_ADMIN = os.environ.get("TOKEN_ADMIN", "")        # authorized role for ADMIN_ROUTE (BFLA control)
RES = "/documents"                                     # resource collection route
ADMIN_ROUTE = "/admin/export"                          # privileged function, member must be denied
ADMIN_METHOD = os.environ.get("ADMIN_METHOD", "GET")   # HTTP method of the privileged function
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
    try:
        yield body
    finally:
        # Teardown must never mask the test's own failure: report leftovers as a warning.
        try:
            d = call("DELETE", f"{RES}/{body['id']}", TOKEN_OWNER)
            if not 200 <= d.status_code < 300:
                warnings.warn(f"teardown: DELETE returned {d.status_code}; leftover resource id={body['id']}")
        except requests.RequestException as exc:
            warnings.warn(f"teardown: DELETE failed ({exc!r}); leftover resource id={body['id']}")


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
    if not TOKEN_ADMIN:
        pytest.skip("TOKEN_ADMIN not set: without an authorized-role positive control the BFLA denial proves nothing")
    # Positive control: an authorized role reaches the function.
    assert call(ADMIN_METHOD, ADMIN_ROUTE, TOKEN_ADMIN).status_code == 200, "admin control failed: wrong ADMIN_ROUTE or ADMIN_METHOD?"
    r = call(ADMIN_METHOD, ADMIN_ROUTE, TOKEN_MEMBER)
    assert r.status_code in DENIED, f"BFLA: member reached {ADMIN_ROUTE} with {r.status_code}"


def test_mass_assignment_protected_fields_ignored_or_rejected(doc):
    path = f"{RES}/{doc['id']}"
    before = call("GET", path, TOKEN_OWNER).json()
    # Bare field name only: the response schema decides whether it is visible.
    readable = {f: v for f, v in PROTECTED_FIELDS.items() if f in before}
    if not readable:
        pytest.skip(f"none of {sorted(PROTECTED_FIELDS)} is readable in the response: outcome cannot be proven")
    r = call("PUT", path, TOKEN_OWNER, json={"title": "Renamed", **PROTECTED_FIELDS})
    assert r.status_code in (200, 204, 400, 403, 422), f"unexpected status {r.status_code}"
    # Re-read the resource: the PUT response alone says nothing about persisted state.
    after = call("GET", path, TOKEN_OWNER).json()
    for field, injected in readable.items():
        assert field in after, f"mass assignment: {field} vanished from the re-read resource"
        assert after[field] == before[field], f"mass assignment: {field} changed to {after[field]!r}"
        assert after[field] != injected
