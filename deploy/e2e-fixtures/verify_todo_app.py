#!/usr/bin/env python3
"""Unabhängige Black-box-Akzeptanzprüfung für den isolierten To-do-E2E-Workspace."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path


ROOT = Path(__file__).resolve().parent
DATA = Path(tempfile.mkdtemp(prefix="todo-e2e-data-")) / "todos.json"
ENV = {**os.environ, "TODO_DATA_FILE": str(DATA)}


def fail(message: str) -> None:
    raise AssertionError(message)


def command(*args: str, expected: int = 0) -> str:
    result = subprocess.run(
        [sys.executable, "todo_cli.py", *args], cwd=ROOT, env=ENV,
        capture_output=True, text=True,
    )
    if result.returncode != expected:
        fail(f"CLI {' '.join(args)} returned {result.returncode}: {result.stderr}")
    return result.stdout


def request(url: str, method: str = "GET", body: dict | None = None) -> tuple[int, object]:
    raw = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=raw, method=method)
    if raw is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=5) as response:
            return response.status, json.loads(response.read().decode())
    except urllib.error.HTTPError as error:
        return error.code, json.loads(error.read().decode() or "{}")


def main() -> None:
    required = ["todo_app.py", "todo_cli.py", "index.html", "README.md", "tests/test_todo_app.py"]
    missing = [name for name in required if not (ROOT / name).is_file()]
    if missing:
        fail("missing delivery files: " + ", ".join(missing))

    # Keep the design contract objective enough to catch the common
    # placeholder outcome without dictating one visual style or framework.
    html = (ROOT / "index.html").read_text(encoding="utf-8").lower()
    readme = (ROOT / "README.md").read_text(encoding="utf-8").lower()
    for marker in (":root", "--", ":focus-visible", "prefers-reduced-motion", "aria-live"):
        if marker not in html:
            fail(f"design/accessibility contract missing: {marker}")
    if "<label" not in html:
        fail("design/accessibility contract missing visible form labels")
    if "design" not in readme:
        fail("README must document the deliberate design plan")

    created = json.loads(command("add", "Plan release checklist", "--description", "Document the ship", "--priority", "high", "--tag", "release", "--tag", "docs", "--due", "2030-01-15"))
    task_id = str(created["id"])
    assert created["status"] == "open"
    assert created["priority"] == "high"
    assert set(created["tags"]) == {"release", "docs"}
    listed = json.loads(command("list", "--tag", "release", "--query", "checklist"))
    assert [item["id"] for item in listed] == [task_id]
    assert json.loads(command("complete", task_id))["status"] == "done"
    assert json.loads(command("reopen", task_id))["status"] == "open"
    command("add", "Fix overdue item", "--priority", "urgent", "--tag", "bug")
    assert len(json.loads(command("list", "--priority", "urgent"))) == 1
    command("delete", task_id)
    command("show", task_id, expected=1)

    server = subprocess.Popen(
        [sys.executable, "todo_app.py", "serve", "--port", "0"], cwd=ROOT, env=ENV,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    try:
        base = ""
        deadline = time.time() + 8
        while time.time() < deadline:
            line = server.stdout.readline().strip() if server.stdout else ""
            if line.startswith("LISTENING http://"):
                base = line.split(" ", 1)[1]
                break
        if not base:
            fail("server did not announce LISTENING URL")
        status, health = request(base + "/healthz")
        assert status == 200 and health == {"status": "ok"}
        status, task = request(base + "/api/tasks", "POST", {"title": "API task", "priority": "normal", "tags": ["api"]})
        assert status in (200, 201) and task["title"] == "API task"
        status, tasks = request(base + "/api/tasks?tag=api")
        assert status == 200 and any(item["id"] == task["id"] for item in tasks)
        status, done = request(base + f"/api/tasks/{task['id']}/complete", "POST", {})
        assert status == 200 and done["status"] == "done"
        with urllib.request.urlopen(base + "/", timeout=5) as response:
            page = response.read().decode().lower()
        assert "todo" in page and "api task" in page
    finally:
        server.terminate()
        try:
            server.wait(timeout=5)
        except subprocess.TimeoutExpired:
            server.kill()
    print("TODO_E2E_APP_ACCEPTANCE_OK")


if __name__ == "__main__":
    main()
