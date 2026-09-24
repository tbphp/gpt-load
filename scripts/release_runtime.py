"""Run candidate and previous release images against disposable databases."""

from __future__ import annotations

from contextlib import AbstractContextManager
from dataclasses import dataclass
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile
import time
from typing import Callable
from urllib.error import HTTPError
from urllib.request import Request, urlopen
import uuid


class ReleaseRuntimeError(RuntimeError):
    pass


def _docker(*args: str, timeout: int = 180) -> str:
    try:
        result = subprocess.run(
            ["docker", *args], capture_output=True, text=True, timeout=timeout, check=False
        )
    except subprocess.TimeoutExpired as error:
        raise ReleaseRuntimeError(f"Docker {args[0]} 超时") from error
    if result.returncode:
        raise ReleaseRuntimeError(
            f"Docker {args[0]} 失败（退出码 {result.returncode}）；原始输出已隐藏以保护凭据"
        )
    return result.stdout.strip()


def _write_env(path: Path, values: dict[str, str]) -> None:
    # Values are generated locally; never echo the file or include secrets in a report.
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        for key, value in values.items():
            if "\n" in value or "\r" in value:
                raise ReleaseRuntimeError(f"环境变量 {key} 含换行")
            stream.write(f"{key}={value}\n")


def _request(
    base_url: str, method: str, path: str, auth_key: str, body: dict | None = None
) -> dict:
    data = None if body is None else json.dumps(body).encode("utf-8")
    headers = {"Authorization": f"Bearer {auth_key}"}
    if data is not None:
        headers["Content-Type"] = "application/json"
        headers["Idempotency-Key"] = str(uuid.uuid4())
    request = Request(base_url + path, data=data, headers=headers, method=method)
    try:
        with urlopen(request, timeout=12) as response:
            payload = json.load(response)
    except HTTPError as error:
        raise ReleaseRuntimeError(f"{method} {path} 返回 HTTP {error.code}") from error
    except (OSError, ValueError) as error:
        raise ReleaseRuntimeError(f"{method} {path} 请求或响应无效") from error
    if not isinstance(payload, dict) or payload.get("code") != 0:
        raise ReleaseRuntimeError(f"{method} {path} 返回失败业务状态")
    return payload


def _wait_for_health(base_url: str, expected_version: str) -> None:
    for _ in range(120):
        try:
            with urlopen(base_url + "/health", timeout=2) as response:
                payload = json.load(response)
            if payload.get("status") == "ok" and payload.get("version") == expected_version:
                return
        except (OSError, ValueError):
            pass
        time.sleep(0.5)
    raise ReleaseRuntimeError("应用未在 60 秒内报告预期版本的健康状态")


@dataclass(frozen=True)
class Seed:
    group_id: int
    group_name: str
    access_key_name: str
    access_key_value: str


class DockerDatabase(AbstractContextManager["DockerDatabase"]):
    def __init__(self, driver: str):
        if driver not in {"sqlite", "mysql", "postgres"}:
            raise ValueError(driver)
        self.driver = driver
        self.suffix = uuid.uuid4().hex[:12]
        self.network = f"gpt-load-pre-release-{self.suffix}"
        self.data_volume = f"gpt-load-pre-release-data-{self.suffix}"
        self.database = f"gpt-load-pre-release-db-{self.suffix}"
        self.app = f"gpt-load-pre-release-app-{self.suffix}"
        self.auth_key = secrets.token_urlsafe(32)
        self.encryption_key = secrets.token_urlsafe(32)
        self.db_password = secrets.token_urlsafe(24)
        self.temporary = tempfile.TemporaryDirectory(prefix="gpt-load-release-db-")
        self.app_env = Path(self.temporary.name) / "app.env"
        self.db_env = Path(self.temporary.name) / "db.env"
        self.base_url = ""
        self._owned: list[tuple[str, str]] = []

    def __enter__(self) -> "DockerDatabase":
        try:
            _docker("network", "create", self.network)
            self._owned.append(("network", self.network))
            _docker("volume", "create", self.data_volume)
            self._owned.append(("volume", self.data_volume))
            if self.driver != "sqlite":
                self._start_database()
            values = {
                "HOST": "0.0.0.0",
                "PORT": "3001",
                "DATA_DIR": "/app/data",
                "AUTH_KEY": self.auth_key,
                "ENCRYPTION_KEY": self.encryption_key,
                "DATABASE_DSN": self._database_dsn(),
            }
            _write_env(self.app_env, values)
            return self
        except Exception:
            self.__exit__(None, None, None)
            raise

    def _database_dsn(self) -> str:
        if self.driver == "sqlite":
            return ""
        if self.driver == "mysql":
            return (
                f"mysql://root:{self.db_password}@{self.database}:3306/gpt_load"
                "?charset=utf8mb4&transaction_isolation=%27READ-COMMITTED%27"
            )
        return (
            f"postgres://postgres:{self.db_password}@{self.database}:5432/gpt_load"
            "?sslmode=disable"
        )

    def _start_database(self) -> None:
        if self.driver == "mysql":
            image = "mysql:8.4"
            data_path = "/var/lib/mysql"
            environment = {"MYSQL_ROOT_PASSWORD": self.db_password, "MYSQL_DATABASE": "gpt_load"}
            readiness = ("mysqladmin", "ping", "-h", "127.0.0.1")
        else:
            image = "postgres:18"
            data_path = "/var/lib/postgresql"
            environment = {"POSTGRES_PASSWORD": self.db_password, "POSTGRES_DB": "gpt_load"}
            readiness = ("pg_isready", "-U", "postgres", "-d", "gpt_load")
        _write_env(self.db_env, environment)
        _docker(
            "run", "-d", "--name", self.database, "--network", self.network,
            "--env-file", str(self.db_env), "--tmpfs", f"{data_path}:rw,nosuid,nodev,size=2g",
            image,
        )
        self._owned.append(("container", self.database))
        for _ in range(120):
            result = subprocess.run(
                ["docker", "exec", self.database, *readiness],
                capture_output=True, timeout=5, check=False,
            )
            if result.returncode == 0:
                return
            time.sleep(0.5)
        raise ReleaseRuntimeError(f"{self.driver} 数据库未在 60 秒内就绪")

    def start_app(self, image: str, expected_version: str) -> str:
        if self.base_url:
            raise ReleaseRuntimeError("应用容器已运行")
        _docker(
            "run", "-d", "--name", self.app, "--network", self.network,
            "--env-file", str(self.app_env),
            "--volume", f"{self.data_volume}:/app/data",
            "--publish", "127.0.0.1:0:3001", image,
        )
        if ("container", self.app) not in self._owned:
            self._owned.append(("container", self.app))
        binding = _docker("port", self.app, "3001/tcp")
        if not binding.startswith("127.0.0.1:") or "\n" in binding:
            raise ReleaseRuntimeError("应用端口没有安全发布到本机回环地址")
        self.base_url = "http://" + binding
        _wait_for_health(self.base_url, expected_version)
        return self.base_url

    def stop_app(self) -> None:
        if not self.base_url:
            return
        _docker("stop", "--time", "15", self.app)
        status = _docker("inspect", "-f", "{{.State.ExitCode}}", self.app)
        if status != "0":
            raise ReleaseRuntimeError(f"应用停止状态异常：{status}")
        _docker("rm", self.app)
        self._owned.remove(("container", self.app))
        self.base_url = ""

    def seed(self) -> Seed:
        name = f"Release Fixture {self.suffix}"
        group = _request(
            self.base_url, "POST", "/api/groups", self.auth_key,
            {
                "name": name,
                "channel_id": "openai_compatible",
                "connection_type": "api_key",
                "params": {"base_url": "http://fake-upstream:8080/v1"},
                "models": [{"id": "release-fixture-model", "alias": "", "alias_enabled": False}],
                "credentials": "release-fixture-upstream-key",
                "confirm_same_target": False,
            },
        )
        if not isinstance(group.get("data"), dict) or not isinstance(
            group["data"].get("group_id"), int
        ):
            raise ReleaseRuntimeError("旧版未创建预期分组")
        key_name = f"Release Fixture Key {self.suffix}"
        access = _request(
            self.base_url, "POST", "/api/access-keys", self.auth_key,
            {"name": key_name},
        )
        if not isinstance(access.get("data"), dict) or not access["data"].get("key"):
            raise ReleaseRuntimeError("旧版未创建预期访问密钥")
        return Seed(group["data"]["group_id"], name, key_name, access["data"]["key"])

    def verify(self, seed: Seed) -> None:
        groups = _request(self.base_url, "GET", "/api/groups", self.auth_key)
        access = _request(self.base_url, "GET", "/api/access-keys", self.auth_key)
        group_items = groups.get("data", {}).get("items")
        access_items = access.get("data", {}).get("items")
        if not isinstance(group_items, list) or not any(
            isinstance(item, dict) and item.get("name") == seed.group_name
            for item in group_items
        ):
            raise ReleaseRuntimeError("升级后分组数据缺失")
        if not isinstance(access_items, list) or not any(
            isinstance(item, dict) and item.get("name") == seed.access_key_name
            for item in access_items
        ):
            raise ReleaseRuntimeError("升级后访问密钥数据缺失")

    def write_after_upgrade(self) -> None:
        name = f"Release Upgrade Write {uuid.uuid4().hex[:10]}"
        result = _request(
            self.base_url, "POST", "/api/access-keys", self.auth_key, {"name": name}
        )
        if not isinstance(result.get("data"), dict) or not result["data"].get("key"):
            raise ReleaseRuntimeError("迁移后无法创建访问密钥")
        items = _request(self.base_url, "GET", "/api/access-keys", self.auth_key).get(
            "data", {}
        ).get("items")
        if not isinstance(items, list) or not any(
            isinstance(item, dict) and item.get("name") == name for item in items
        ):
            raise ReleaseRuntimeError("迁移后的新访问密钥未持久化")

    def start_fake_stream(self, image: str) -> None:
        server = f"gpt-load-pre-release-fake-{self.suffix}"
        body = Path(self.temporary.name) / "stream.txt"
        body.write_text(
            'data: {"id":"release-stream","object":"chat.completion.chunk",'
            '"created":1,"model":"release-fixture-model","choices":'
            '[{"index":0,"delta":{"content":"release-ok"},"finish_reason":null}]}\n\n'
            'data: {"id":"release-stream","object":"chat.completion.chunk",'
            '"created":1,"model":"release-fixture-model","choices":'
            '[{"index":0,"delta":{},"finish_reason":"stop"}]}\n\n'
            "data: [DONE]\n\n",
            encoding="utf-8",
        )
        handler = Path(self.temporary.name) / "respond.sh"
        handler.write_text(
            "#!/bin/sh\n"
            "length=0\n"
            "while IFS= read -r line; do\n"
            "  case \"$line\" in\n"
            "    [Cc]ontent-[Ll]ength:*) length=\"$(printf '%s' \"${line#*:}\" | tr -d '[:space:]')\" ;;\n"
            "    \"$(printf '\\r')\" | '') break ;;\n"
            "  esac\n"
            "done\n"
            "case \"$length\" in '' | *[!0-9]*) length=0 ;; esac\n"
            "if [ \"$length\" -gt 0 ]; then dd bs=1 count=\"$length\" of=/dev/null 2>/dev/null; fi\n"
            "size=\"$(wc -c </tmp/stream.txt)\"\n"
            "printf 'HTTP/1.1 200 OK\\r\\nContent-Type: text/event-stream\\r\\nContent-Length: %s\\r\\nConnection: close\\r\\n\\r\\n' \"$size\"\n"
            "cat /tmp/stream.txt\n",
            encoding="utf-8",
        )
        os.chmod(handler, 0o555)
        os.chmod(body, 0o444)
        _docker(
            "run", "-d", "--name", server, "--network", self.network,
            "--network-alias", "fake-upstream",
            "--volume", f"{body}:/tmp/stream.txt:ro",
            "--volume", f"{handler}:/tmp/respond:ro",
            "--entrypoint", "/bin/sh", image,
            "-ceu", "exec nc -lk -p 8080 -e /tmp/respond",
        )
        self._owned.append(("container", server))
        for _ in range(40):
            result = subprocess.run(
                ["docker", "exec", server, "nc", "-z", "127.0.0.1", "8080"],
                capture_output=True, timeout=3, check=False,
            )
            if result.returncode == 0:
                return
            time.sleep(0.25)
        raise ReleaseRuntimeError("假上游流式服务未就绪")

    def verify_stream(self, seed: Seed) -> None:
        body = json.dumps(
            {
                "model": "release-fixture-model",
                "stream": True,
                "messages": [{"role": "user", "content": "release probe"}],
            }
        ).encode("utf-8")
        request = Request(
            self.base_url + "/v1/chat/completions", data=body, method="POST",
            headers={
                "Authorization": f"Bearer {seed.access_key_value}",
                "Content-Type": "application/json",
            },
        )
        try:
            with urlopen(request, timeout=30) as response:
                content_type = response.headers.get("Content-Type", "")
                content = response.read().decode("utf-8")
        except HTTPError as error:
            raise ReleaseRuntimeError(f"流式请求返回 HTTP {error.code}") from error
        except (OSError, UnicodeDecodeError) as error:
            raise ReleaseRuntimeError("流式请求或响应无效") from error
        if "text/event-stream" not in content_type or "release-ok" not in content or "data: [DONE]" not in content:
            raise ReleaseRuntimeError("流式请求没有返回完整 SSE 内容")

    def __exit__(self, exc_type, exc_value, traceback) -> None:
        failures = []
        for resource_type, name in reversed(self._owned):
            command = ("rm", "-f", name) if resource_type == "container" else (resource_type, "rm", name)
            try:
                _docker(*command)
            except ReleaseRuntimeError:
                failures.append(name)
        self._owned.clear()
        self.temporary.cleanup()
        if failures:
            raise ReleaseRuntimeError("临时 Docker 资源清理失败：" + ", ".join(failures)) from exc_value


def run_database_matrix(
    candidate_image: str,
    candidate_version: str,
    previous_tag: str,
    browser_check: Callable[[str, str, Seed], None] | None = None,
) -> None:
    previous_image = f"ghcr.io/tbphp/gpt-load:{previous_tag.removeprefix('v')}"
    _docker("pull", previous_image, timeout=600)
    for driver in ("sqlite", "mysql", "postgres"):
        print(f"[数据库] {driver} 当前版本全新启动")
        with DockerDatabase(driver) as database:
            database.start_app(candidate_image, candidate_version)
            seed = database.seed()
            database.verify(seed)
            if driver == "sqlite" and browser_check is not None:
                browser_check(database.base_url, database.auth_key, seed)
            if driver == "sqlite":
                database.start_fake_stream(candidate_image)
                database.verify_stream(seed)
            database.stop_app()
            database.start_app(candidate_image, candidate_version)
            database.verify(seed)
            database.stop_app()
        print(f"[数据库] {driver} 上一版本升级")
        with DockerDatabase(driver) as database:
            database.start_app(previous_image, previous_tag)
            seed = database.seed()
            database.verify(seed)
            database.stop_app()
            database.start_app(candidate_image, candidate_version)
            database.verify(seed)
            database.write_after_upgrade()
            database.stop_app()
            database.start_app(candidate_image, candidate_version)
            database.verify(seed)
            database.stop_app()


def run_compose_smoke(source: Path, candidate_image: str, candidate_version: str) -> None:
    project = "gptloadprerelease" + uuid.uuid4().hex[:10]
    with tempfile.TemporaryDirectory(prefix="gpt-load-release-compose-") as temporary:
        directory = Path(temporary)
        shutil.copyfile(source / "docker-compose.yml", directory / "docker-compose.yml")
        _write_env(
            directory / ".env",
            {
                "AUTH_KEY": secrets.token_urlsafe(32),
                "ENCRYPTION_KEY": secrets.token_urlsafe(32),
                "DATABASE_DSN": "",
                "PORT": "3001",
            },
        )
        override = directory / "release-override.yml"
        override.write_text(
            "services:\n"
            "  gpt-load:\n"
            f"    image: {candidate_image}\n"
            "    ports: !override\n"
            "      - '127.0.0.1:0:3001'\n"
            "    restart: 'no'\n",
            encoding="utf-8",
        )
        command = (
            "compose", "--project-directory", str(directory), "-p", project,
            "-f", str(directory / "docker-compose.yml"), "-f", str(override),
        )
        try:
            _docker(*command, "up", "-d", "--wait", timeout=180)
            binding = _docker(*command, "port", "gpt-load", "3001")
            if not binding.startswith("127.0.0.1:") or "\n" in binding:
                raise ReleaseRuntimeError("Compose 主端口没有绑定本机回环地址")
            _wait_for_health("http://" + binding, candidate_version)
        finally:
            # `up` can partially create containers or volumes even when it fails.
            _docker(*command, "down", "--volumes", "--remove-orphans", timeout=90)
