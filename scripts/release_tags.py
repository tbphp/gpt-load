"""Remote tag inspection and release version selection."""

from functools import cmp_to_key
from pathlib import Path
import re
import runpy


_semver = runpy.run_path(
    str(Path(__file__).resolve().parents[1] / ".github/scripts/release-compare-semver.py")
)
compare_versions = _semver["compare_versions"]
parse_version = _semver["parse_version"]
_release_tag = re.compile(r"^v2\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$")
_numbered_prerelease = re.compile(r"^(beta|rc)\.(0|[1-9][0-9]*)$")


class ReleaseTagError(ValueError):
    pass


def parse_remote_tags(output: str) -> dict[str, str]:
    tags: dict[str, str] = {}
    for line in output.splitlines():
        try:
            oid, ref = line.split("\t", 1)
        except ValueError as error:
            raise ReleaseTagError("远端 tag 列表格式无效") from error
        if not ref.startswith("refs/tags/") or ref.endswith("^{}"):
            continue
        name = ref.removeprefix("refs/tags/")
        if name.startswith("v2."):
            tags[name] = oid
    return tags


def select_latest_tag(tags: dict[str, str]) -> str:
    if not tags:
        raise ReleaseTagError("远端没有可用的 2.x 发布 tag")
    for tag in tags:
        if not _release_tag.fullmatch(tag):
            raise ReleaseTagError(f"远端存在无效的 2.x tag：{tag}")
        try:
            parse_version(tag)
        except ValueError as error:
            raise ReleaseTagError(f"远端存在无效的 2.x tag：{tag}") from error
    return max(tags, key=cmp_to_key(compare_versions))


def suggest_next_tag(latest: str) -> str:
    major, minor, patch, prerelease = parse_version(latest)
    if major != 2:
        raise ReleaseTagError("当前 Release 只支持 2.x tag")
    if not prerelease:
        return f"v{major}.{minor}.{patch + 1}"
    suffix = latest.split("-", 1)[1]
    match = _numbered_prerelease.fullmatch(suffix)
    if match is None:
        raise ReleaseTagError("无法从非 beta/rc 的预发布 tag 推断下一版本")
    return f"v{major}.{minor}.{patch}-{match.group(1)}.{int(match.group(2)) + 1}"


def validate_new_tag(tag: str, latest: str, existing: dict[str, str]) -> None:
    if not _release_tag.fullmatch(tag):
        raise ReleaseTagError("tag 不符合当前 Release 工作流的 2.x 格式")
    try:
        parse_version(tag)
        comparison = compare_versions(tag, latest)
    except ValueError as error:
        raise ReleaseTagError(f"tag 版本无效：{tag}") from error
    if tag in existing:
        raise ReleaseTagError(f"tag 已存在：{tag}")
    if comparison <= 0:
        raise ReleaseTagError(f"tag 必须高于远端最新版本 {latest}")


def verify_local_remote_tags(local: dict[str, str], remote: dict[str, str]) -> None:
    for name, oid in local.items():
        if name not in remote:
            raise ReleaseTagError(f"本地存在远端没有的发布 tag：{name}")
        if remote[name] != oid:
            raise ReleaseTagError(f"本地与远端 tag 指向不同对象：{name}")
    for name in remote:
        if name not in local:
            raise ReleaseTagError(f"远端发布 tag 尚未同步到本地：{name}")
