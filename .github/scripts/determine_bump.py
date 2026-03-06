#!/usr/bin/env python3
"""Ask Claude to determine the semver bump type from merged PRs."""
import sys
import os
import json
import urllib.request
import subprocess

prev_tag = sys.argv[1]
since = os.environ["SINCE"]
repo = os.environ["REPO"]


def run_gh(*args):
    result = subprocess.run(["gh"] + list(args), capture_output=True, text=True)
    try:
        data = json.loads(result.stdout) if result.stdout.strip() else []
        return data if isinstance(data, list) else []
    except (json.JSONDecodeError, ValueError):
        return []


prs = run_gh(
    "pr", "list", "--repo", repo,
    "--state", "merged",
    "--search", "merged:>=" + since,
    "--json", "number,title,body",
    "--limit", "100",
)

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
with urllib.request.urlopen(req) as resp:
    data = json.loads(resp.read())
print(data["content"][0]["text"].strip().upper())
