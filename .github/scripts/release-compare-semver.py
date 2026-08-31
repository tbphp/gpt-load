#!/usr/bin/env python3

import re
import sys


VERSION_PATTERN = re.compile(
    r"^v?(0|[1-9][0-9]*)\."
    r"(0|[1-9][0-9]*)\."
    r"(0|[1-9][0-9]*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)


def parse_version(raw: str) -> tuple[int, int, int, list[tuple[bool, int | str]]]:
    match = VERSION_PATTERN.fullmatch(raw)
    if match is None:
        raise ValueError(f"invalid semantic version: {raw!r}")

    prerelease: list[tuple[bool, int | str]] = []
    raw_prerelease = match.group(4)
    if raw_prerelease is not None:
        for identifier in raw_prerelease.split("."):
            if identifier.isascii() and identifier.isdigit():
                if len(identifier) > 1 and identifier.startswith("0"):
                    raise ValueError(
                        f"numeric prerelease identifier has a leading zero: {raw!r}"
                    )
                prerelease.append((True, int(identifier)))
            else:
                prerelease.append((False, identifier))

    return int(match.group(1)), int(match.group(2)), int(match.group(3)), prerelease


def compare_identifiers(
    left: tuple[bool, int | str], right: tuple[bool, int | str]
) -> int:
    left_numeric, left_value = left
    right_numeric, right_value = right
    if left_numeric != right_numeric:
        return -1 if left_numeric else 1
    return (left_value > right_value) - (left_value < right_value)


def compare_versions(left_raw: str, right_raw: str) -> int:
    left_major, left_minor, left_patch, left_prerelease = parse_version(left_raw)
    right_major, right_minor, right_patch, right_prerelease = parse_version(right_raw)

    left_core = (left_major, left_minor, left_patch)
    right_core = (right_major, right_minor, right_patch)
    if left_core != right_core:
        return (left_core > right_core) - (left_core < right_core)

    if not left_prerelease and not right_prerelease:
        return 0
    if not left_prerelease:
        return 1
    if not right_prerelease:
        return -1

    for left_identifier, right_identifier in zip(left_prerelease, right_prerelease):
        comparison = compare_identifiers(left_identifier, right_identifier)
        if comparison != 0:
            return comparison
    return (len(left_prerelease) > len(right_prerelease)) - (
        len(left_prerelease) < len(right_prerelease)
    )


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: release-compare-semver.py LEFT RIGHT", file=sys.stderr)
        return 2
    try:
        comparison = compare_versions(sys.argv[1], sys.argv[2])
    except ValueError as error:
        print(error, file=sys.stderr)
        return 2
    print(comparison)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
