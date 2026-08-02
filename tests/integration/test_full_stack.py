"""Cross-service integration tests against two real processes — `python -m
app.server` and `go run ./cmd/gateway`, communicating over a real gRPC
connection, fronted by a real HTTP server — not the in-process fakes
`gateway-go/internal/httpserver`'s own tests use for `search.Searcher`/
`rag.Answerer`/etc., and not the in-process gRPC server
`tests/unit/ml_service`'s tests use.

This automates what was, until this phase, only ever done manually: every
phase in this project's build history was verified against a live
gateway-go + ml-service pair via ad-hoc `curl` commands, the results
described in that phase's commit message but never captured as a
repeatable test. This file is that verification, codified.

Requires: a Go toolchain, and a `python` with ml-service installed on
PATH (set `ML_SERVICE_PYTHON` to override which `python` — see
`_python_executable` below; defaults to `ml-service/.venv`'s if present,
else whatever `python` resolves to, which is how `.github/workflows/
ci.yml`'s separate `integration` job runs this against the runner's
system Python rather than a venv). Kept as its own CI job rather than
folded into `ci.yml`'s ml-service job — a real Go process, a real gRPC
connection, and both languages' proto codegen all have to succeed before
this runs, which is a slower, different failure mode than a unit test's.

    cd ml-service && .venv/Scripts/python.exe -m pytest ../tests/integration -v
"""

from __future__ import annotations

import os
import platform
import socket
import subprocess
import sys
import time
from collections.abc import Iterator
from pathlib import Path

import pytest
import requests

_REPO_ROOT = Path(__file__).resolve().parents[2]
_ML_SERVICE_DIR = _REPO_ROOT / "ml-service"
_GATEWAY_GO_DIR = _REPO_ROOT / "gateway-go"

_ML_SERVICE_GRPC_PORT = 50199
_ML_SERVICE_METRICS_PORT = 9199
_GATEWAY_HTTP_PORT = 8199
_GATEWAY_METRICS_PORT = 9198

_STARTUP_TIMEOUT_S = 30


def _python_executable() -> str:
    override = os.environ.get("ML_SERVICE_PYTHON")
    if override:
        return override
    venv_python = _ML_SERVICE_DIR / ".venv" / "Scripts" / "python.exe"
    if venv_python.is_file():
        return str(venv_python)
    venv_python_posix = _ML_SERVICE_DIR / ".venv" / "bin" / "python"
    if venv_python_posix.is_file():
        return str(venv_python_posix)
    return sys.executable


def _terminate_process_tree(proc: subprocess.Popen) -> None:
    """proc.terminate() alone isn't enough for the gateway-go process:
    `go run` compiles to a temp binary and execs it as a *child* process,
    and killing the `go run` wrapper doesn't reliably kill that child on
    Windows (no process-group/job-object relationship by default) — found
    by an orphaned gateway-go process still listening on
    _GATEWAY_HTTP_PORT after a normal `proc.terminate()` + `wait()`
    completed cleanly. `taskkill /T` explicitly kills the whole tree;
    POSIX's plain terminate() doesn't have this problem in the first
    place (go run's child is in the same process group by default there).
    """
    if platform.system() == "Windows":
        subprocess.run(
            ["taskkill", "/F", "/T", "/PID", str(proc.pid)],
            check=False,  # already exiting/gone is not an error worth failing teardown over
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    else:
        proc.terminate()
    try:
        proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        proc.kill()


def _wait_for_port(host: str, port: int, timeout_s: float) -> None:
    deadline = time.monotonic() + timeout_s
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        try:
            with socket.create_connection((host, port), timeout=1):
                return
        except OSError as exc:
            last_error = exc
            time.sleep(0.5)
    raise TimeoutError(
        f"nothing listening on {host}:{port} after {timeout_s}s"
    ) from last_error


def _wait_for_http_ok(url: str, timeout_s: float) -> None:
    deadline = time.monotonic() + timeout_s
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        try:
            resp = requests.get(url, timeout=2)
            if resp.status_code == 200:
                return
        except requests.RequestException as exc:
            last_error = exc
        time.sleep(0.5)
    raise TimeoutError(f"{url} never returned 200 within {timeout_s}s") from last_error


@pytest.fixture(scope="module")
def stack(tmp_path_factory) -> Iterator[str]:
    """Starts a real ml-service and a real gateway-go, yields gateway-go's
    base URL, tears both down afterward. Module-scoped: every test in this
    file shares one live pair rather than paying startup cost per test."""
    vector_store_dir = tmp_path_factory.mktemp("vector-store")
    storage_dir = tmp_path_factory.mktemp("documents")

    ml_service_env = {
        **os.environ,
        "ML_SERVICE_GRPC_PORT": str(_ML_SERVICE_GRPC_PORT),
        "ML_SERVICE_METRICS_PORT": str(_ML_SERVICE_METRICS_PORT),
        "ML_SERVICE_VECTOR_STORE_DIR": str(vector_store_dir),
        "ML_SERVICE_LOG_LEVEL": "warning",
    }
    ml_service_proc = subprocess.Popen(
        [_python_executable(), "-m", "app.server"],
        cwd=_ML_SERVICE_DIR,
        env=ml_service_env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )

    gateway_env = {
        **os.environ,
        "GATEWAY_HTTP_PORT": str(_GATEWAY_HTTP_PORT),
        "GATEWAY_METRICS_PORT": str(_GATEWAY_METRICS_PORT),
        "GATEWAY_ML_SERVICE_ADDR": f"127.0.0.1:{_ML_SERVICE_GRPC_PORT}",
        "GATEWAY_STORAGE_DIR": str(storage_dir),
        "GATEWAY_ADMIN_EMAIL": "admin@integration-test.local",
        "GATEWAY_ADMIN_PASSWORD": "integration-test-admin-password",
        "GATEWAY_LOG_LEVEL": "error",
        # This module's five tests share one live gateway-go (see the
        # module-scoped `stack` fixture below) and collectively make more
        # requests in quick succession than GATEWAY_RATE_LIMIT_RPS's
        # default (5 rps, burst 10) tolerates — found via a real flaky
        # 429 on the evaluate test. Rate limiting itself is already
        # covered live by internal/httpserver's own tests
        # (TestRateLimit_ExceedingBurstReturns429); this suite isn't
        # testing that, so it shouldn't be incidentally throttled by it.
        "GATEWAY_RATE_LIMIT_RPS": "1000",
        "GATEWAY_RATE_LIMIT_BURST": "1000",
    }
    gateway_proc = None

    try:
        _wait_for_port("127.0.0.1", _ML_SERVICE_GRPC_PORT, _STARTUP_TIMEOUT_S)

        gateway_proc = subprocess.Popen(
            ["go", "run", "./cmd/gateway"],
            cwd=_GATEWAY_GO_DIR,
            env=gateway_env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        base_url = f"http://127.0.0.1:{_GATEWAY_HTTP_PORT}"
        _wait_for_http_ok(f"{base_url}/healthz", _STARTUP_TIMEOUT_S)

        yield base_url
    finally:
        for proc in (gateway_proc, ml_service_proc):
            if proc is not None:
                _terminate_process_tree(proc)


def _register_and_login(base_url: str, email: str, password: str) -> str:
    resp = requests.post(
        f"{base_url}/api/v1/auth/register", json={"email": email, "password": password}
    )
    assert resp.status_code == 201, resp.text
    resp = requests.post(
        f"{base_url}/api/v1/auth/login", json={"email": email, "password": password}
    )
    assert resp.status_code == 200, resp.text
    return resp.json()["access_token"]


def test_register_login_me_flow(stack: str):
    token = _register_and_login(
        stack, "integration-user1@test.local", "correct-horse-battery"
    )

    resp = requests.get(
        f"{stack}/api/v1/me", headers={"Authorization": f"Bearer {token}"}
    )
    assert resp.status_code == 200
    assert resp.json()["email"] == "integration-user1@test.local"
    assert resp.json()["role"] == "user"


def test_upload_extract_embed_and_search_flow(stack: str):
    token = _register_and_login(
        stack, "integration-user2@test.local", "correct-horse-battery"
    )

    upload_resp = requests.post(
        f"{stack}/api/v1/documents",
        headers={"Authorization": f"Bearer {token}"},
        files={
            "file": (
                "tesla.txt",
                b"Tesla automotive revenue grew eighteen percent year over year.",
            )
        },
    )
    assert upload_resp.status_code == 202, upload_resp.text
    document_id = upload_resp.json()["document_id"]

    deadline = time.monotonic() + 20
    status = None
    while time.monotonic() < deadline:
        doc_resp = requests.get(
            f"{stack}/api/v1/documents/{document_id}",
            headers={"Authorization": f"Bearer {token}"},
        )
        assert doc_resp.status_code == 200
        status = doc_resp.json()["job"]["status"]
        if status in ("completed", "failed"):
            break
        time.sleep(0.5)
    assert status == "completed", f"job never completed, last status={status!r}"

    search_resp = requests.get(
        f"{stack}/api/v1/search",
        params={"q": "tesla revenue", "mode": "hybrid"},
        headers={"Authorization": f"Bearer {token}"},
    )
    assert search_resp.status_code == 200
    results = search_resp.json()["results"]
    assert any(r["document_id"] == document_id for r in results)


def test_rag_query_streams_tokens_and_cites_the_uploaded_document(stack: str):
    token = _register_and_login(
        stack, "integration-user3@test.local", "correct-horse-battery"
    )

    upload_resp = requests.post(
        f"{stack}/api/v1/documents",
        headers={"Authorization": f"Bearer {token}"},
        files={
            "file": (
                "apple.txt",
                b"Apple iPhone revenue grew due to strong holiday demand.",
            )
        },
    )
    document_id = upload_resp.json()["document_id"]

    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        doc_resp = requests.get(
            f"{stack}/api/v1/documents/{document_id}",
            headers={"Authorization": f"Bearer {token}"},
        )
        if doc_resp.json()["job"]["status"] == "completed":
            break
        time.sleep(0.5)

    resp = requests.post(
        f"{stack}/api/v1/rag/query",
        headers={"Authorization": f"Bearer {token}"},
        json={"question": "How did Apple's revenue perform?"},
        stream=True,
        timeout=15,
    )
    assert resp.status_code == 200
    assert resp.headers["Content-Type"].startswith("text/event-stream")

    body = resp.text
    assert "event:token" in body
    assert "event:final" in body
    assert (
        document_id in body
    )  # the final event's citation should reference this document


def test_admin_endpoints_require_admin_role(stack: str):
    token = _register_and_login(
        stack, "integration-user4@test.local", "correct-horse-battery"
    )

    resp = requests.get(
        f"{stack}/api/v1/admin/users", headers={"Authorization": f"Bearer {token}"}
    )
    assert resp.status_code == 403

    admin_login = requests.post(
        f"{stack}/api/v1/auth/login",
        json={
            "email": "admin@integration-test.local",
            "password": "integration-test-admin-password",
        },
    )
    admin_token = admin_login.json()["access_token"]

    resp = requests.get(
        f"{stack}/api/v1/admin/users",
        headers={"Authorization": f"Bearer {admin_token}"},
    )
    assert resp.status_code == 200
    assert any(
        u["email"] == "admin@integration-test.local" for u in resp.json()["users"]
    )


def test_admin_evaluate_discriminates_grounded_from_hallucinated(stack: str):
    admin_login = requests.post(
        f"{stack}/api/v1/auth/login",
        json={
            "email": "admin@integration-test.local",
            "password": "integration-test-admin-password",
        },
    )
    admin_token = admin_login.json()["access_token"]
    headers = {"Authorization": f"Bearer {admin_token}"}

    grounded = requests.post(
        f"{stack}/api/v1/admin/evaluate",
        headers=headers,
        json={
            "question": "How did Tesla revenue perform?",
            "answer": "Tesla revenue grew eighteen percent year over year.",
            "context": [
                "Tesla automotive revenue grew eighteen percent year over year."
            ],
        },
    ).json()
    hallucinated = requests.post(
        f"{stack}/api/v1/admin/evaluate",
        headers=headers,
        json={
            "question": "How did Tesla revenue perform?",
            "answer": "Tesla announced a new CEO and a stock split.",
            "context": [
                "Tesla automotive revenue grew eighteen percent year over year."
            ],
        },
    ).json()

    assert grounded["faithfulness"] > hallucinated["faithfulness"]
