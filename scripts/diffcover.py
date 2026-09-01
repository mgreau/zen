#!/usr/bin/env python3
"""Diff coverage: what fraction of the lines this branch adds are covered by tests.

Whole-project coverage lets a well-covered core hide an untested new feature,
which is the case worth catching. This measures only lines the diff adds.

Usage:
    go test ./... -coverprofile=coverage.out
    scripts/diffcover.py --profile coverage.out --base origin/main --min 75

Exits non-zero when coverage of added lines falls below --min.
"""

import argparse
import collections
import os
import re
import subprocess
import sys

# "path/file.go:12.34,56.7 3 1" -> file, start line, end line, statements, hits
PROFILE_RE = re.compile(r"^(?P<file>.+):(?P<sl>\d+)\.\d+,(?P<el>\d+)\.\d+ \d+ (?P<count>\d+)$")
HUNK_RE = re.compile(r"^@@ -\d+(?:,\d+)? \+(?P<start>\d+)(?:,(?P<len>\d+))? @@")


def module_path():
    """The Go module path, used to turn profile paths into repo-relative ones."""
    with open("go.mod", encoding="utf-8") as fh:
        for line in fh:
            if line.startswith("module "):
                return line.split(None, 1)[1].strip()
    return ""


def covered_and_uncovered(profile):
    """Map repo-relative file -> {line: covered}.

    A line inside any executed block counts as covered. Go's profile is
    block-based, so a line can appear in several blocks; covered wins, which
    matches how `go tool cover` renders it.
    """
    prefix = module_path() + "/"
    lines = collections.defaultdict(dict)
    with open(profile, encoding="utf-8") as fh:
        for raw in fh:
            raw = raw.strip()
            if not raw or raw.startswith("mode:"):
                continue
            m = PROFILE_RE.match(raw)
            if not m:
                continue
            path = m.group("file")
            if prefix and path.startswith(prefix):
                path = path[len(prefix):]
            hit = int(m.group("count")) > 0
            for ln in range(int(m.group("sl")), int(m.group("el")) + 1):
                lines[path][ln] = lines[path].get(ln, False) or hit
    return lines


def added_lines(base):
    """Map repo-relative .go file -> set of line numbers the diff adds."""
    diff = subprocess.run(
        ["git", "diff", "--unified=0", "--diff-filter=d", f"{base}...HEAD", "--", "*.go"],
        capture_output=True, text=True, check=True,
    ).stdout
    added = collections.defaultdict(set)
    path = None
    lineno = 0
    for line in diff.splitlines():
        if line.startswith("+++ b/"):
            path = line[6:]
            continue
        m = HUNK_RE.match(line)
        if m:
            lineno = int(m.group("start"))
            continue
        if line.startswith("+") and not line.startswith("+++") and path:
            added[path].add(lineno)
            lineno += 1
    return added


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--profile", default="coverage.out")
    ap.add_argument("--base", default="origin/main")
    ap.add_argument("--min", type=float, default=75.0)
    args = ap.parse_args()

    if not os.path.exists(args.profile):
        print(f"coverage profile not found: {args.profile}", file=sys.stderr)
        return 2

    profile = covered_and_uncovered(args.profile)
    added = added_lines(args.base)

    total = hit = 0
    misses = collections.defaultdict(list)
    for path, lines in sorted(added.items()):
        # Test files are the instrument, not the subject. Lines with no
        # coverage record are not statements (imports, types, comments,
        # braces) and cannot be covered either way, so they are excluded
        # rather than counted as misses.
        if path.endswith("_test.go") or path not in profile:
            continue
        for ln in sorted(lines):
            if ln not in profile[path]:
                continue
            total += 1
            if profile[path][ln]:
                hit += 1
            else:
                misses[path].append(ln)

    if total == 0:
        print("diff coverage: no new statements to cover")
        return 0

    pct = 100.0 * hit / total
    print(f"diff coverage: {pct:.1f}% ({hit}/{total} new statements) vs {args.base}")
    if misses:
        print("\nuncovered new lines:")
        for path, lns in sorted(misses.items()):
            print(f"  {path}: {compress(lns)}")

    if pct + 1e-9 < args.min:
        print(f"\nFAIL: diff coverage {pct:.1f}% is below the {args.min:.0f}% threshold", file=sys.stderr)
        return 1
    print(f"\nOK: at or above the {args.min:.0f}% threshold")
    return 0


def compress(nums):
    """Render [1,2,3,7] as '1-3, 7'."""
    out, start, prev = [], nums[0], nums[0]
    for n in nums[1:]:
        if n == prev + 1:
            prev = n
            continue
        out.append(f"{start}-{prev}" if start != prev else f"{start}")
        start = prev = n
    out.append(f"{start}-{prev}" if start != prev else f"{start}")
    return ", ".join(out)


if __name__ == "__main__":
    sys.exit(main())
