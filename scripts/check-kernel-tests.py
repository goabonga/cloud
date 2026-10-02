#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>
"""Reject a green integration run that omitted required kernel tests."""
import json
import sys

required = {"TestExecBackendRealBridge", "TestExecBackendRealNetworkOps", "TestExecFirewallRealChain"}
passed = set()
with open(sys.argv[1], encoding="utf-8") as stream:
    for line in stream:
        event = json.loads(line)
        if event.get("Action") == "pass":
            passed.add(event.get("Test"))
missing = required - passed
if missing:
    sys.exit("Required kernel tests did not pass: " + ", ".join(sorted(missing)))
