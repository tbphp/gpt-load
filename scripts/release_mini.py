"""Check one candidate against an isolated copy of the DBX mini database."""

from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import tempfile
import time
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from scripts.release_runtime import ReleaseRuntimeError, _request, _wait_for_health


class ReleaseMiniError(RuntimeError):
    pass


def _helper_path() -> Path:
    codex_home = Path(os.environ.get("CODEX_HOME", Path.home() / ".codex"))
    return codex_home / "skills/gpt-load-mini-test-db/scripts/manage.py"


def _read_env(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if "=" not in line or line.lstrip().startswith("#"):
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip().strip("\"'")
    return values


def _helper(operation: str, sandbox: Path, helper: Path) -> dict:
    result = subprocess.run(
        ["python3", str(helper), "--repo", str(sandbox), operation],
        capture_output=True, text=True, check=False, timeout=300,
    )
    if result.returncode:
        raise ReleaseMiniError(
            f"mini 隔离副本 {operation} 失败（退出码 {result.returncode}）；诊断已隐藏"
        )
    try:
        payload = json.loads(result.stdout)
    except ValueError as error:
        raise ReleaseMiniError(f"mini 副本 {operation} 没有返回有效状态") from error
    if not isinstance(payload, dict):
        raise ReleaseMiniError(f"mini 副本 {operation} 返回异常状态")
    return payload


def _free_port() -> int:
    with socket.socket() as server:
        server.bind(("127.0.0.1", 0))
        return server.getsockname()[1]


def check_mini_access(source: Path) -> None:
    helper = _helper_path()
    if not helper.is_file():
        raise ReleaseMiniError(f"找不到本机 mini 副本辅助脚本：{helper}")
    if not (source / ".env").is_file():
        raise ReleaseMiniError("当前工作区缺少用于验证 DBX 连接的 .env")
    with tempfile.TemporaryDirectory(prefix="gpt-load-release-mini-check-") as temporary:
        sandbox = Path(temporary)
        shutil.copyfile(source / "go.mod", sandbox / "go.mod")
        shutil.copyfile(source / ".env", sandbox / ".env")
        os.chmod(sandbox / ".env", 0o600)
        _helper("check", sandbox, helper)


def _run_real_request(base_url: str, auth_key: str) -> None:
    access_keys = _request(base_url, "GET", "/api/access-keys?q=dbx&page_size=100", auth_key)
    items = access_keys.get("data", {}).get("items")
    if not isinstance(items, list):
        raise ReleaseMiniError("mini 副本没有返回访问密钥列表")
    matches = [item for item in items if isinstance(item, dict) and item.get("name") == "dbx"]
    if len(matches) != 1 or not isinstance(matches[0].get("id"), int):
        raise ReleaseMiniError("mini 副本中找不到唯一的 dbx 访问密钥")
    revealed = _request(
        base_url, "POST", f"/api/access-keys/{matches[0]['id']}/reveal", auth_key, {}
    )
    key = revealed.get("data", {}).get("key")
    if not isinstance(key, str) or not key:
        raise ReleaseMiniError("无法在隔离副本中读取 dbx 访问密钥")
    body = json.dumps(
        {"model": "gpt-6-luna", "input": "Reply with the word OK.", "stream": False}
    ).encode("utf-8")
    request = Request(
        base_url + "/v1/responses", method="POST", data=body,
        headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"},
    )
    started_ms = int(time.time() * 1000) - 1000
    try:
        with urlopen(request, timeout=120) as response:
            result = json.load(response)
    except HTTPError as error:
        raise ReleaseMiniError(f"真实 Responses 请求返回 HTTP {error.code}") from error
    except (OSError, ValueError) as error:
        raise ReleaseMiniError("真实 Responses 请求失败或响应无效") from error
    if not isinstance(result, dict) or not isinstance(result.get("id"), str):
        raise ReleaseMiniError("真实 Responses 请求缺少响应 ID")
    logs_path = (
        "/api/logs?"
        f"from_ms={started_ms}&access_key_id={matches[0]['id']}"
        "&client_model=gpt-6-luna&protocol=openai-responses&status=success&limit=10"
    )
    for _ in range(30):
        logs = _request(base_url, "GET", logs_path, auth_key)
        items = logs.get("data", {}).get("items")
        if isinstance(items, list) and any(
            isinstance(item, dict)
            and item.get("client_model") == "gpt-6-luna"
            and item.get("status") == "success"
            for item in items
        ):
            return
        time.sleep(0.25)
    raise ReleaseMiniError("真实 Responses 成功响应未写入请求日志")


def run_mini_smoke(
    source: Path, binary: Path, expected_version: str, report_dir: Path
) -> None:
    helper = _helper_path()
    if not helper.is_file():
        raise ReleaseMiniError(f"找不到本机 mini 副本辅助脚本：{helper}")
    if not (source / ".env").is_file():
        raise ReleaseMiniError("当前工作区缺少用于验证 DBX 连接的 .env")
    if not binary.is_file():
        raise ReleaseMiniError("候选原生二进制不存在")
    report_dir.mkdir(parents=True, exist_ok=True)
    os.chmod(report_dir, 0o700)
    sandbox = Path(tempfile.mkdtemp(prefix="gpt-load-release-mini-"))
    log_path = report_dir / "mini-app.log"
    process: subprocess.Popen | None = None
    succeeded = False
    try:
        shutil.copyfile(source / "go.mod", sandbox / "go.mod")
        shutil.copyfile(source / ".env", sandbox / ".env")
        os.chmod(sandbox / ".env", 0o600)
        _helper("check", sandbox, helper)
        clone = _helper("create", sandbox, helper)
        if clone.get("status") != "created":
            raise ReleaseMiniError("mini 隔离副本未成功创建")
        values = _read_env(sandbox / ".env")
        port = _free_port()
        environment = os.environ.copy()
        environment.update(
            {
                "HOST": "127.0.0.1",
                "PORT": str(port),
                "DATA_DIR": str(sandbox / "data"),
                "DATABASE_DSN": values["DATABASE_DSN"],
                "ENCRYPTION_KEY": values["ENCRYPTION_KEY"],
            }
        )
        if values.get("AUTH_KEY"):
            environment["AUTH_KEY"] = values["AUTH_KEY"]
        else:
            environment.pop("AUTH_KEY", None)
        descriptor = os.open(log_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as log:
            process = subprocess.Popen(
                [str(binary)], cwd=sandbox, env=environment, stdout=log, stderr=subprocess.STDOUT
            )
            base_url = f"http://127.0.0.1:{port}"
            _wait_for_health(base_url, expected_version)
            auth_key = values.get("AUTH_KEY") or (sandbox / "data/auth.key").read_text().strip()
            _run_real_request(base_url, auth_key)
            succeeded = True
    except ReleaseRuntimeError as error:
        raise ReleaseMiniError(str(error)) from error
    finally:
        if process is not None:
            process.send_signal(signal.SIGTERM)
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        if (sandbox / ".env.mini-test-db-state.json").exists():
            try:
                _helper("cleanup", sandbox, helper)
            except ReleaseMiniError:
                # Keep the skill's ownership marker and original .env for recovery.
                raise ReleaseMiniError(
                    f"mini 副本清理失败；已保留恢复状态：{sandbox}"
                )
        shutil.rmtree(sandbox)
        if succeeded:
            log_path.unlink(missing_ok=True)
