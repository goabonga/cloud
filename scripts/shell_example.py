#!/usr/bin/env python3

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Pipeline test: run a command through the shell, which bandit rejects."""

import subprocess
import sys

subprocess.run("echo " + " ".join(sys.argv[1:]), shell=True, check=True)
