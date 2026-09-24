"""Run browser acceptance against a disposable candidate instance."""

from pathlib import Path
import os
import subprocess

from scripts.release_runtime import Seed


class ReleaseBrowserError(RuntimeError):
    pass


def run_browser_smoke(
    source: Path, base_url: str, auth_key: str, seed: Seed, report_dir: Path
) -> None:
    package = source / "scripts/release-e2e"
    report_dir.mkdir(parents=True, exist_ok=True)
    os.chmod(report_dir, 0o700)
    environment = os.environ.copy()
    environment.update(
        {
            "RELEASE_TEST_BASE_URL": base_url,
            "RELEASE_TEST_AUTH_KEY": auth_key,
            "RELEASE_TEST_GROUP_ID": str(seed.group_id),
            "RELEASE_TEST_GROUP_NAME": seed.group_name,
            "RELEASE_TEST_ACCESS_KEY_NAME": seed.access_key_name,
            "RELEASE_TEST_ACCESS_KEY_VALUE": seed.access_key_value,
            "RELEASE_E2E_REPORT": str(report_dir / "html"),
            "RELEASE_E2E_OUTPUT": str(report_dir / "results"),
        }
    )
    for command in (
        ["corepack", "pnpm", "--dir", str(package), "install", "--frozen-lockfile"],
        ["corepack", "pnpm", "--dir", str(package), "exec", "playwright", "install", "chromium"],
        ["corepack", "pnpm", "--dir", str(package), "test"],
    ):
        result = subprocess.run(
            command, cwd=source, env=environment, capture_output=True, text=True, check=False
        )
        if result.returncode:
            # Synthetic local credentials can still appear in browser traces. Keep
            # diagnostics on this machine and avoid pasting raw output to the terminal.
            log_path = report_dir / "browser-failure.log"
            descriptor = os.open(log_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
                stream.write(result.stdout)
                stream.write(result.stderr)
            raise ReleaseBrowserError(
                f"浏览器验收失败（退出码 {result.returncode}）；诊断：{log_path}"
            )
