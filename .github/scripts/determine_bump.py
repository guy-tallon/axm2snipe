#!/usr/bin/env python3
"""Ask Claude to determine the semver bump type from merged PRs."""
import sys
import os
import json
import urllib.request
import urllib.error
import subprocess

prev_tag = sys.argv[1]
since = os.environ["SINCE"]
repo = os.environ["REPO"]

result = subprocess.run(
    ["gh", "pr", "list", "--repo", repo,
     "--state", "merged",
     "--search", "merged:>=" + since,
     "--json", "number,title,body",
     "--limit", "100"],
    capture_output=True, text=True
)
if result.returncode != 0:
    print("Could not fetch PRs: " + result.stderr.strip(), file=sys.stderr)
    print("PATCH")
    sys.exit(0)

try:
    prs = json.loads(result.stdout) if result.stdout.strip() else []
    if not isinstance(prs, list):
        prs = []
except (json.JSONDecodeError, ValueError):
    prs = []

if not prs:
    print("NONE")
    sys.exit(0)

pr_text = "\n\n".join(
    "PR #" + str(pr["number"]) + ": " + pr["title"] + "\n" + (pr.get("body") or "").strip()
    for pr in prs
)

prompt = (
    "You are a semantic versioning expert. Given the following merged pull requests "
    "for a Go CLI tool (axm2snipe), determine the appropriate semver bump type.\n\n"
    "Current version: " + prev_tag + "\n\n"
    "Merged pull requests:\n" + pr_text + "\n\n"
    "Semver rules:\n"
    "- MAJOR: breaking changes, removed features, incompatible config/API changes\n"
    "- MINOR: new features, new commands, new config options (backwards compatible)\n"
    "- PATCH: bug fixes, performance improvements, docs, tests, refactoring (no new features)\n\n"
    "Respond with exactly one word: MAJOR, MINOR, or PATCH."
)

payload = {
    "model": "claude-haiku-4-5-20251001",
    "max_tokens": 10,
    "messages": [{"role": "user", "content": prompt}],
}

try:
    req = urllib.request.Request(
        "https://api.anthropic.com/v1/messages",
        data=json.dumps(payload).encode(),
        headers={
            "x-api-key": os.environ["ANTHROPIC_API_KEY"],
            "anthropic-version": "2023-06-01",
            "content-type": "application/json",
        },
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        data = json.loads(resp.read())
    text = data.get("content", [{}])[0].get("text", "PATCH").strip().upper()
    print(text if text in ("MAJOR", "MINOR", "PATCH") else "PATCH")
except Exception as e:
    print("API error: " + str(e), file=sys.stderr)
    print("PATCH")
