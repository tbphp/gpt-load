"""Prepare one immutable source revision for release acceptance."""

from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path
import subprocess
import sys
import tempfile
from typing import Iterator

from scripts.release_tags import (
    ReleaseTagError,
    parse_remote_tags,
    select_latest_tag,
    suggest_next_tag,
    validate_new_tag,
    verify_local_remote_tags,
)


class ReleaseSourceError(RuntimeError):
    pass


class ReleaseCancelled(ReleaseSourceError):
    pass


@dataclass(frozen=True)
class PreparedSource:
    repository: Path
    worktree: Path
    candidate_sha: str
    latest_tag: str
    remote_tags: dict[str, str]


def _git(repository: Path, *args: str) -> str:
    result = subprocess.run(
        ["git", *args], cwd=repository, capture_output=True, text=True, check=False
    )
    if result.returncode != 0:
        action = " ".join(args[:2])
        raise ReleaseSourceError(f"Git 操作失败：{action}（退出码 {result.returncode}）")
    return result.stdout.strip()


def _remote_main(repository: Path) -> str:
    output = _git(repository, "ls-remote", "origin", "refs/heads/main")
    if not output:
        raise ReleaseSourceError("远端缺少 main 分支")
    oid, ref = output.split("\t", 1)
    if ref != "refs/heads/main":
        raise ReleaseSourceError("远端 main 引用格式无效")
    return oid


def _remote_tags(repository: Path) -> dict[str, str]:
    return parse_remote_tags(
        _git(repository, "ls-remote", "--tags", "origin", "refs/tags/v2.*")
    )


def _local_tags(repository: Path) -> dict[str, str]:
    return parse_remote_tags(
        _git(
            repository,
            "for-each-ref",
            "--format=%(objectname)%09%(refname)",
            "refs/tags/v2.*",
        )
    )


@contextmanager
def prepare_source(repository: Path, *, local_head: bool = False) -> Iterator[PreparedSource]:
    repository = repository.resolve()
    if Path(_git(repository, "rev-parse", "--show-toplevel")) != repository:
        raise ReleaseSourceError("发版入口必须位于仓库根目录")
    if local_head and _git(repository, "status", "--porcelain"):
        raise ReleaseSourceError("模拟验收不包含未提交改动，请先提交当前改动")

    # Fetch only changes Git refs. It leaves every checkout and local edit untouched.
    main_before = _remote_main(repository)
    remote_before = _remote_tags(repository)
    for tag, local_oid in _local_tags(repository).items():
        if tag in remote_before and remote_before[tag] != local_oid:
            raise ReleaseSourceError(f"本地与远端 tag 冲突：{tag}")
    _git(repository, "fetch", "origin", "main", "--tags")
    local = _local_tags(repository)
    remote = _remote_tags(repository)
    try:
        verify_local_remote_tags(local, remote)
        latest = select_latest_tag(remote)
    except ReleaseTagError as error:
        raise ReleaseSourceError(str(error)) from error
    remote_main = _git(repository, "rev-parse", "refs/remotes/origin/main")
    if remote_main != main_before or _remote_main(repository) != remote_main:
        raise ReleaseSourceError("远端 main 在同步期间发生变化，请重新运行")
    if remote != remote_before:
        raise ReleaseSourceError("远端 tag 在同步期间发生变化，请重新运行")
    candidate = _git(repository, "rev-parse", "HEAD") if local_head else remote_main
    _git(repository, "merge-base", "--is-ancestor", latest, candidate)

    with tempfile.TemporaryDirectory(prefix="gpt-load-release-") as temporary:
        worktree = Path(temporary) / "source"
        _git(repository, "worktree", "add", "--detach", str(worktree), candidate)
        try:
            yield PreparedSource(repository, worktree, candidate, latest, remote)
        finally:
            _git(repository, "worktree", "remove", "--force", str(worktree))


def verify_source_unchanged(prepared: PreparedSource) -> None:
    if _remote_main(prepared.repository) != prepared.candidate_sha:
        raise ReleaseSourceError("远端 main 已改变，本次验收不能用于打 tag")
    if _remote_tags(prepared.repository) != prepared.remote_tags:
        raise ReleaseSourceError("远端发布 tag 已改变，本次验收不能用于打 tag")


def prompt_for_tag(prepared: PreparedSource, input_fn=input) -> str:
    if input_fn is input and not sys.stdin.isatty():
        raise ReleaseCancelled("没有交互式终端，不能确认发版 tag")
    try:
        suggested = suggest_next_tag(prepared.latest_tag)
    except ReleaseTagError:
        suggested = None
    print(f"验收 SHA：{prepared.candidate_sha}")
    print(f"远端最新 tag：{prepared.latest_tag}")
    if suggested:
        print(f"建议 tag：{suggested}")
    else:
        print("该预发布类型没有安全的自动建议，请手动输入完整 tag。")
    choice = input_fn("输入完整 tag，回车采用建议值，或输入 q 取消：").strip()
    if choice.lower() in {"q", "quit"}:
        raise ReleaseCancelled("用户取消创建 tag")
    tag = choice or suggested
    if tag is None:
        raise ReleaseSourceError("必须输入完整 tag")
    try:
        validate_new_tag(tag, prepared.latest_tag, prepared.remote_tags)
    except ReleaseTagError as error:
        raise ReleaseSourceError(str(error)) from error
    print(f"将把 {tag} 指向 {prepared.candidate_sha} 并推送到 origin。")
    if input_fn("输入 yes 确认创建并推送：").strip() != "yes":
        raise ReleaseCancelled("用户未确认创建 tag")
    return tag


def publish_tag(prepared: PreparedSource, tag: str) -> None:
    verify_source_unchanged(prepared)
    try:
        validate_new_tag(tag, prepared.latest_tag, prepared.remote_tags)
    except ReleaseTagError as error:
        raise ReleaseSourceError(str(error)) from error
    if tag in _local_tags(prepared.repository):
        raise ReleaseSourceError(f"本地已有 tag：{tag}，不会覆盖")
    _git(prepared.repository, "tag", tag, prepared.candidate_sha)
    try:
        _git(prepared.repository, "push", "origin", f"refs/tags/{tag}")
    except ReleaseSourceError as error:
        raise ReleaseSourceError(
            f"推送 {tag} 失败；本地 tag 已创建，请先核对远端状态，不要自动重试或改写 tag"
        ) from error
    local_oid = _local_tags(prepared.repository).get(tag)
    remote_oid = _remote_tags(prepared.repository).get(tag)
    commit = _git(prepared.repository, "rev-parse", f"{tag}^{{commit}}")
    if remote_oid != local_oid or commit != prepared.candidate_sha:
        raise ReleaseSourceError(
            f"{tag} 已推送，但无法确认远端指向；请核对远端状态，不要自动重试"
        )
