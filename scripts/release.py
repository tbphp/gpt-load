"""One local release command for GPT-Load. No AI decisions occur at runtime."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

from scripts.release_browser import run_browser_smoke
from scripts.release_git import (
    ReleaseCancelled,
    PreparedSource,
    prepare_source,
    prompt_for_tag,
    publish_tag,
)
from scripts.release_mini import check_mini_access, run_mini_smoke
from scripts.release_runtime import Seed, run_compose_smoke, run_database_matrix


REPOSITORY = "tbphp/gpt-load"


class ReleaseToolError(RuntimeError):
    pass


def successful_release_run(payload: dict, tag: str) -> int:
    runs = payload.get("workflow_runs")
    if not isinstance(runs, list):
        raise ReleaseToolError("无法查询上一版本的 Release 状态")
    for run in runs:
        if (
            isinstance(run, dict)
            and run.get("head_branch") == tag
            and run.get("status") == "completed"
            and run.get("conclusion") == "success"
            and isinstance(run.get("id"), int)
        ):
            return run["id"]
    raise ReleaseToolError(f"{tag} 没有成功完成的 Release 记录，不能跳过它继续发版")


def _command(*args: str, cwd: Path | None = None, timeout: int = 30) -> str:
    result = subprocess.run(
        list(args), cwd=cwd, capture_output=True, text=True, check=False, timeout=timeout
    )
    if result.returncode:
        raise ReleaseToolError(f"{args[0]} {args[1] if len(args) > 1 else ''} 失败（退出码 {result.returncode}）")
    return result.stdout.strip()


def _github_json(*args: str) -> dict:
    try:
        value = json.loads(_command("gh", *args, timeout=30))
    except ValueError as error:
        raise ReleaseToolError("GitHub 返回的数据不是有效 JSON") from error
    if not isinstance(value, dict):
        raise ReleaseToolError("GitHub 返回的数据结构无效")
    return value


def _release_runs(sha: str) -> dict:
    return _github_json(
        "api", f"repos/{REPOSITORY}/actions/workflows/release.yml/runs?head_sha={sha}&per_page=100"
    )


def _verify_previous_release(prepared: PreparedSource) -> None:
    commit = _command("git", "rev-parse", f"{prepared.latest_tag}^{{commit}}", cwd=prepared.worktree)
    if commit == prepared.candidate_sha:
        raise ReleaseToolError("远端 main 与上一发布 tag 指向同一 commit，没有新的发布内容")
    successful_release_run(_release_runs(commit), prepared.latest_tag)
    release = _github_json(
        "release", "view", prepared.latest_tag, "--repo", REPOSITORY,
        "--json", "isDraft,targetCommitish",
    )
    if release.get("isDraft") is not False or release.get("targetCommitish") != commit:
        raise ReleaseToolError(f"{prepared.latest_tag} 不是指向预期 commit 的公开 Release")
    _command(
        str(prepared.worktree / ".github/scripts/release-verify-image-revision.sh"),
        f"ghcr.io/tbphp/gpt-load:{prepared.latest_tag.removeprefix('v')}",
        commit,
        cwd=prepared.worktree,
        timeout=90,
    )


def _preflight(repository: Path, prepared: PreparedSource) -> None:
    for command in ("git", "gh", "docker", "go", "corepack", "python3", "jq"):
        if shutil.which(command) is None:
            raise ReleaseToolError(f"缺少必要命令：{command}")
    _command("docker", "info", "--format", "{{.ServerVersion}}")
    _command("docker", "compose", "version")
    _command("gh", "auth", "status")
    slug = _command("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner", cwd=prepared.worktree)
    if slug != REPOSITORY:
        raise ReleaseToolError(f"当前仓库不是 {REPOSITORY}")
    if not (repository / ".env").is_file():
        raise ReleaseToolError("当前工作区缺少 mini 验收需要的 .env")
    launcher_files = {
        path.name: path.read_bytes()
        for path in (repository / "scripts").glob("release*.py")
    }
    candidate_files = {
        path.name: path.read_bytes()
        for path in (prepared.worktree / "scripts").glob("release*.py")
    }
    if not launcher_files or launcher_files != candidate_files:
        raise ReleaseToolError("本地发版工具与远端 main 不一致，请先更新发版工具再运行")
    _verify_previous_release(prepared)
    check_mini_access(repository)


def _report_directory(sha: str) -> Path:
    parent = Path.home() / ".cache/gpt-load/release-acceptance"
    parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(parent, 0o700)
    path = Path(tempfile.mkdtemp(prefix=f"{sha[:12]}-", dir=parent))
    os.chmod(path, 0o700)
    return path


def _save_report(path: Path, payload: dict) -> None:
    destination = path / "summary.json"
    temporary = path / "summary.json.tmp"
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        json.dump(payload, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
    os.replace(temporary, destination)


def _logged_command(path: Path, label: str, command: list[str], source: Path, timeout: int) -> None:
    log_path = path / f"{label}.log"
    descriptor = os.open(log_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        result = subprocess.run(
            command, cwd=source, stdout=stream, stderr=subprocess.STDOUT,
            check=False, timeout=timeout,
        )
    if result.returncode:
        raise ReleaseToolError(f"{label} 失败（退出码 {result.returncode}）；诊断：{log_path}")


def _stage(path: Path, summary: dict, label: str, action) -> None:
    print(f"[验收] {label}", flush=True)
    summary["checks"][label] = "running"
    _save_report(path, summary)
    try:
        action()
    except Exception as error:
        summary["checks"][label] = "failed"
        summary["failed_stage"] = label
        summary["reason"] = str(error)
        _save_report(path, summary)
        raise
    summary["checks"][label] = "passed"
    _save_report(path, summary)


def run_release(repository: Path, *, simulate: bool = False) -> int:
    with prepare_source(repository, local_head=simulate) as prepared:
        report_dir = _report_directory(prepared.candidate_sha)
        summary: dict = {
            "mode": "simulation" if simulate else "release",
            "candidate_sha": prepared.candidate_sha,
            "previous_tag": prepared.latest_tag,
            "checks": {},
            "tag_pushed": False,
        }
        _save_report(report_dir, summary)
        print(f"候选 commit：{prepared.candidate_sha}")
        print(f"上一发布 tag：{prepared.latest_tag}")
        print(f"本机报告：{report_dir}")
        image = f"gpt-load-pre-release:{prepared.candidate_sha[:12]}-{os.getpid()}"
        version = f"v2.0.0-preflight.{prepared.candidate_sha[:12]}"
        binary = prepared.worktree / "tmp/gpt-load-release-candidate"
        image_built = False
        try:
            _stage(report_dir, summary, "发版环境与上一版本", lambda: _preflight(repository, prepared))
            binary.parent.mkdir(exist_ok=True)
            _stage(
                report_dir, summary, "构建候选原生程序",
                lambda: _logged_command(
                    report_dir, "build-native",
                    ["go", "build", "-ldflags", f"-X gpt-load/internal/platform/version.Version={version}", "-o", str(binary), "."],
                    prepared.worktree, 600,
                ),
            )
            _stage(
                report_dir, summary, "构建候选镜像",
                lambda: _logged_command(
                    report_dir, "build-image",
                    ["docker", "build", "--build-arg", f"VERSION={version}", "-t", image, "."],
                    prepared.worktree, 1800,
                ),
            )
            image_built = True

            def browser(base_url: str, auth_key: str, seed: Seed) -> None:
                run_browser_smoke(
                    prepared.worktree, base_url, auth_key, seed, report_dir / "browser"
                )

            _stage(
                report_dir, summary, "三驱动新装与旧版升级、流式和主要页面",
                lambda: run_database_matrix(image, version, prepared.latest_tag, browser),
            )
            _stage(
                report_dir, summary, "Docker Compose 实际启动",
                lambda: run_compose_smoke(prepared.worktree, image, version),
            )
            _stage(
                report_dir, summary, "mini 真实数据与 Responses 上游",
                lambda: run_mini_smoke(repository, binary, version, report_dir / "mini"),
            )
        finally:
            if image_built:
                subprocess.run(["docker", "image", "rm", image], capture_output=True, check=False)

        print(f"主要页面截图报告：{report_dir / 'browser/html/index.html'}")
        if simulate:
            summary["simulation_status"] = "passed"
            _save_report(report_dir, summary)
            print("本地模拟验收通过；没有创建或推送 tag。")
            return 0
        print("请在确认 tag 前查看截图与变更说明。")
        commits = _command(
            "git", "log", "--format=%h %s", f"{prepared.latest_tag}..{prepared.candidate_sha}",
            cwd=prepared.worktree,
        )
        print(commits[:4000])
        tag = prompt_for_tag(prepared)
        summary["tag"] = tag
        summary["tag_pushed"] = None  # Until the remote push is independently verified.
        _save_report(report_dir, summary)
        publish_tag(prepared, tag)
        summary["tag_pushed"] = True
        summary["release_status"] = "not_checked"
        _save_report(report_dir, summary)
        print(f"{tag} 已推送；现有 Release workflow 将按原配置运行，请在 GitHub 查看结果。")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="GPT-Load 本地发版验收")
    parser.add_argument("--simulate", action="store_true", help="验收当前已提交的 HEAD，不创建或推送 tag")
    options = parser.parse_args()
    try:
        return run_release(Path.cwd(), simulate=options.simulate)
    except ReleaseCancelled as error:
        print(f"已取消：{error}", file=sys.stderr)
        return 2
    except Exception as error:
        print(f"发版已停止：{error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
