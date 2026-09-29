import os
import time
from urllib.parse import urlparse
import requests
from toxiproxy import Toxiproxy

# EXAMPLE values only; target owners must provide bounded SLOs and endpoint provenance.
TARGET = {
    "toxiproxy_url": os.getenv("TOXIPROXY_URL", "http://localhost:8474"),
    "service_url": os.getenv("SERVICE_URL", "http://localhost:8000/api/checkout"),
    "proxy_name": os.getenv("PROXY_NAME", "upstream_dependency"),
    "request_timeout": float(os.getenv("REQUEST_TIMEOUT", "2")),
    "failure_count": int(os.getenv("FAILURE_COUNT", "5")),
    "fail_fast_ms": float(os.getenv("FAIL_FAST_MS", "25")),
    "sleep_window": float(os.getenv("SLEEP_WINDOW", "5")),
    "accepted_fallback": os.getenv("ACCEPTED_FALLBACK", "202,503").split(","),
}

def validate_target(target):
    if not all(target.get(k) for k in ("toxiproxy_url", "service_url", "proxy_name")):
        raise ValueError("toxiproxy_url, service_url, and proxy_name are required")
    if target["request_timeout"] <= 0 or target["failure_count"] < 1:
        raise ValueError("timeouts and failure_count must be bounded positive values")

# EXAMPLE oracle: replace with target-owned body/schema and breaker-state evidence.
def validate_response(response, expected="closed"):
    body = response.json()
    return response.status_code == 200 and body.get("state") == expected and body.get("ok") is True

def validate_fallback(response):
    return response.status_code in {int(x) for x in TARGET["accepted_fallback"]} and response.json().get("fallback") is True

def observe_state(response):
    return response.headers.get("X-Circuit-State") or response.json().get("state")

def test_circuit_breaker_resilience(target=TARGET):
    validate_target(target)
    endpoint = urlparse(target["toxiproxy_url"])
    client = Toxiproxy(host=endpoint.hostname, port=endpoint.port or 8474)
    proxy = client.get_proxy(target["proxy_name"])
    owned = "gentle-runtime-latency-spike"
    timeout = target["request_timeout"]
    try:
        baseline = requests.post(target["service_url"], json={"amount": 10}, timeout=timeout)
        assert validate_response(baseline), "baseline semantic/state oracle failed"
        proxy.add_toxic(name=owned, type="latency", stream="upstream", attributes={"latency": 3000, "jitter": 100})
        for _ in range(target["failure_count"]):
            requests.post(target["service_url"], json={"amount": 10}, timeout=timeout)
        started = time.perf_counter()
        fast_fail = requests.post(target["service_url"], json={"amount": 10}, timeout=timeout)
        elapsed_ms = (time.perf_counter() - started) * 1000
        assert elapsed_ms < target["fail_fast_ms"], f"fail-fast bound exceeded: {elapsed_ms:.2f}ms"
        assert validate_fallback(fast_fail) and observe_state(fast_fail) in {"OPEN", "open"}, "fallback/state oracle failed"
        time.sleep(target["sleep_window"])
        recovery = requests.post(target["service_url"], json={"amount": 10}, timeout=timeout)
        assert validate_response(recovery, "closed"), "recovery semantic/state oracle failed"
    finally:
        try:
            proxy.destroy_toxic(owned)
        except Exception:
            pass

if __name__ == "__main__":
    test_circuit_breaker_resilience()
