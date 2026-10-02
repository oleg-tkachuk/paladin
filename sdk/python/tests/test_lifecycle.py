"""Processes that make and drop SDK clients and transfers exit cleanly.

A consumer saw ``recursive_mutex lock failed`` abort a test process at exit
on macOS, in 3 of 28 runs with SDK 0.17 loaded. This runs
``lifecycle_stress.py`` — sync and async, plaintext and mutual TLS — in fresh
interpreters and fails on any run that does not exit 0 or that prints an
abort. PALADIN_LIFECYCLE_RUNS sets how many: a few in every test run, 200 in
the CI job that exists for it.
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

RUNS_ENV = "PALADIN_LIFECYCLE_RUNS"
DEFAULT_RUNS = 5
ROUNDS_PER_RUN = 3
# A run is a few seconds; this bounds one that hangs at exit.
RUN_TIMEOUT = 120
STRESS = Path(__file__).resolve().parent / "lifecycle_stress.py"
# What an abort at exit prints, from libc++ or from CPython.
ABORTS = ("recursive_mutex", "Fatal Python error", "terminating due to uncaught exception")


def test_processes_using_the_sdk_exit_cleanly() -> None:
    runs = int(os.environ.get(RUNS_ENV, DEFAULT_RUNS))
    failures = []
    for run in range(runs):
        done = subprocess.run(
            [sys.executable, "-X", "faulthandler", str(STRESS), str(ROUNDS_PER_RUN)],
            cwd=STRESS.parent,
            capture_output=True,
            text=True,
            timeout=RUN_TIMEOUT,
            check=False,
        )
        aborted = [a for a in ABORTS if a in done.stderr]
        if done.returncode != 0 or aborted:
            failures.append(f"run {run}: exit {done.returncode}, {aborted}: {done.stderr[-500:]}")
    assert not failures, f"{len(failures)} of {runs} runs did not exit cleanly:\n" + "\n".join(
        failures
    )
