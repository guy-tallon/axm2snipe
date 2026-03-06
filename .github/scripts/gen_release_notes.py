#!/usr/bin/env python3
"""Generate AI-written release notes using merged PRs and Claude."""
import sys
import os
import json
import urllib.request
import subprocess

version = sys.argv[1]
prev = sys.argv[2]
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
    "--json", "number,title,author,body,mergedAt",
    "--limit", "100",
)

contributors = sorted({
    pr["author"]["login"]
    for pr in prs
    if pr.get("author") and pr["author"].get("login")
    and not pr["author"]["login"].endswith("[bot]")
})

pr_lines = "\n".join(
    "- #" + str(pr["number"]) + ": " + pr["title"] +
    " (@" + (pr["author"]["login"] if pr.get("author") and pr["author"].get("login") else "unknown") + ")"
    for pr in sorted(prs, key=lambda p: p["number"])
)

prev_prs = run_gh(
    "pr", "list", "--repo", repo,
    "--state", "merged",
    "--search", "merged:<" + since,
    "--json", "author",
    "--limit", "500",
)
prev_contributors = {
    p["author"]["login"] for p in prev_prs
    if p.get("author") and p["author"].get("login")
}
new_contributors = [c for c in contributors if c not in prev_contributors]

repo_url = "https://github.com/" + repo
contributors_str = ", ".join("@" + c for c in contributors) or "none"
new_contributors_str = ", ".join("@" + c for c in new_contributors) if new_contributors else "none"
pr_lines_str = pr_lines if pr_lines else "(no PRs found)"

prompt = (
    "You are writing GitHub release notes for axm2snipe, a Go CLI tool that syncs "
    "Apple Business Manager (ABM/ASM) devices into Snipe-IT asset management.\n\n"
    "New version: " + version + "\n"
    "Previous version: " + (prev if prev else "first release") + "\n"
    "Repository: " + repo_url + "\n\n"
    "Merged pull requests in this release:\n" + pr_lines_str + "\n\n"
    "Contributors in this release: " + contributors_str + "\n"
    "New contributors (first-time): " + new_contributors_str + "\n\n"
    "Write concise, user-facing release notes in GitHub Markdown following this structure exactly:\n\n"
    "## What's New\n"
    "(grouped bullet points by theme, written from a user perspective, "
    "referencing PR numbers as links like [#123](url) and @author)\n\n"
    "## Contributors\n"
    "(bullet list of all contributors as @username GitHub profile links; "
    "mark first-time contributors with a party popper emoji)\n\n"
    "**Full Changelog**: " + repo_url + "/compare/" + prev + "..." + version + "\n\n"
    "Be specific but brief. Do not invent features not present in the PR list."
)

payload = {
    "model": "claude-haiku-4-5-20251001",
    "max_tokens": 1024,
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
print(data["content"][0]["text"])
