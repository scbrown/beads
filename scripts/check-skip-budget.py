#!/usr/bin/env python3
"""Fail when a package's test coverage REGRESSES — not when it is merely low.

WHY A BUDGET AND NOT A THRESHOLD (aegis-4mzlq)
==============================================
A zero-test gate catches TOTAL vacuity: a package that ran nothing and printed
`ok`. It is blind to PARTIAL vacuity — a package that used to run 400 tests and
now runs 4 — which is both more common and more insidious, because something ran
so nothing alarms.

An absolute threshold cannot express this. Measured on this repo: 1011 skipped
tests across 12 packages, so any absolute number is either noise today or
unenforceable later. What is meaningful is the DELTA: this package ran 357 tests
yesterday and runs 4 today, in the same environment.

So: a checked-in baseline of ran/skipped per package, and a check that fails when
`ran` drops. Skips going UP is the same event seen from the other side and is
reported, but `ran` is the number that matters — it is the count of things
actually evaluated.

WHY THE ENVIRONMENT FINGERPRINT IS LOAD-BEARING
===============================================
Skip counts are a property of the ENVIRONMENT, not of the code: `internal/
storage/dolt` runs 357 of 774 with no Docker and would run far more with it. A
baseline captured in one environment and compared in another produces confident
nonsense in both directions — a false regression that gets the check disabled, or
a false all-clear that hides a real one.

So the baseline records which test gates were available, and a comparison across
DIFFERENT gates REFUSES rather than reporting. That is the same shape as the
loopback rule this workstream settled on: define the conditions under which the
answer is meaningful, and decline outside them, rather than enumerating the ways
it could be wrong.

Usage:
    check-skip-budget.py --update      # capture a baseline for THIS environment
    check-skip-budget.py               # compare; exit 1 on regression
    check-skip-budget.py --json FILE   # reuse an existing `go test -json` capture
"""

import argparse
import collections
import json
import os
import shutil
import subprocess
import sys

BASELINE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "skip-budget.json")


def environment_fingerprint():
    """Which test gates are satisfied here. Two runs are comparable iff equal."""
    return {
        "docker": shutil.which("docker") is not None
        and subprocess.run(
            ["docker", "info"], capture_output=True
        ).returncode
        == 0,
        "external_dolt": bool(os.environ.get("BEADS_TEST_DOLT_PORT")),
        "dolt_binary": shutil.which("dolt") is not None,
        "skip_list": os.environ.get("BEADS_TEST_SKIP", ""),
    }


def measure(json_path=None):
    """Per-package ran/skipped counts from `go test ./... -json`."""
    if json_path:
        with open(json_path) as fh:
            lines = fh.readlines()
    else:
        proc = subprocess.run(
            ["go", "test", "./...", "-json"], capture_output=True, text=True
        )
        lines = proc.stdout.splitlines()

    pkgs = collections.defaultdict(lambda: {"ran": 0, "skipped": 0, "failed": 0})
    for line in lines:
        try:
            ev = json.loads(line)
        except (ValueError, TypeError):
            continue
        pkg, action, test = ev.get("Package"), ev.get("Action"), ev.get("Test")
        if not pkg or not test:
            continue
        if action == "pass":
            pkgs[pkg]["ran"] += 1
        elif action == "skip":
            pkgs[pkg]["skipped"] += 1
        elif action == "fail":
            pkgs[pkg]["failed"] += 1
    # A failing test still EVALUATED something; it is not vacuity.
    for v in pkgs.values():
        v["ran"] += v["failed"]
    return {k: {"ran": v["ran"], "skipped": v["skipped"]} for k, v in pkgs.items()}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--update", action="store_true", help="capture a new baseline")
    ap.add_argument("--json", help="reuse an existing `go test -json` capture")
    args = ap.parse_args()

    env = environment_fingerprint()
    current = measure(args.json)

    if not current:
        print("REFUSING: measured zero packages — the capture is empty, which is "
              "not the same as 'nothing regressed'.", file=sys.stderr)
        return 2

    if args.update:
        with open(BASELINE, "w") as fh:
            json.dump({"environment": env, "packages": current}, fh,
                      indent=2, sort_keys=True)
            fh.write("\n")
        total = sum(v["ran"] for v in current.values())
        print(f"baseline captured: {len(current)} packages, {total} tests ran")
        print(f"environment: {env}")
        return 0

    if not os.path.exists(BASELINE):
        print(f"REFUSING: no baseline at {BASELINE}. Run --update in the "
              f"environment you intend to guard.", file=sys.stderr)
        return 2

    with open(BASELINE) as fh:
        base = json.load(fh)

    if base.get("environment") != env:
        # The refusal that keeps this honest. Comparing across gates produces a
        # confident wrong answer in BOTH directions.
        print("REFUSING to compare: the test gates differ from the baseline's.",
              file=sys.stderr)
        print(f"  baseline: {base.get('environment')}", file=sys.stderr)
        print(f"  current:  {env}", file=sys.stderr)
        print("  Skip counts are a property of the environment, not the code. "
              "Re-run --update here, or run where the baseline was taken.",
              file=sys.stderr)
        return 2

    regressions = []
    for pkg, was in sorted(base.get("packages", {}).items()):
        now = current.get(pkg)
        if now is None:
            regressions.append((pkg, was["ran"], 0, "package no longer reports tests"))
        elif now["ran"] < was["ran"]:
            regressions.append((pkg, was["ran"], now["ran"], "fewer tests evaluated"))

    if regressions:
        print("FAIL: test coverage REGRESSED — fewer tests are being evaluated "
              "than the baseline for this environment.\n", file=sys.stderr)
        for pkg, before, after, why in regressions:
            print(f"  {pkg}\n      ran {before} -> {after}  ({why})", file=sys.stderr)
        print("\nA package that used to evaluate hundreds of tests and now "
              "evaluates a handful still prints ok. That is what this catches "
              "(aegis-4mzlq).", file=sys.stderr)
        return 1

    new = sorted(set(current) - set(base.get("packages", {})))
    total = sum(v["ran"] for v in current.values())
    print(f"OK: {len(current)} packages, {total} tests evaluated, no package "
          f"evaluates fewer than its baseline.")
    if new:
        print(f"NOTE: {len(new)} package(s) not in the baseline: "
              f"{', '.join(p.rsplit('/', 1)[-1] for p in new[:5])}"
              f"{' …' if len(new) > 5 else ''}. Run --update to adopt them.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
