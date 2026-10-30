import argparse
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import posixpath
import subprocess
import sys


def relevant(path):
    if path in {"README.md", "CHANGELOG.md", "LICENSE", "LICENSE.md", "COPYING"}:
        return False
    if path.startswith("docs/") and path.endswith(".md"):
        return False
    if path.endswith("_test.go") and (not path.startswith("data/") or path.count("/") == 1):
        return False
    return path != "ci/reuse_test.py"


def command(*args):
    return subprocess.check_output(args, stderr=subprocess.PIPE, timeout=30)


def input_key(sha):
    tree = command("git", "ls-tree", "-rz", "--full-tree", sha)
    try:
        embedded = command("git", "grep", "-l", "-F", "//go:embed", sha, "--", "*.go").decode().splitlines()
    except subprocess.CalledProcessError as e:
        if e.returncode != 1:
            raise
        embedded = []
    roots = {posixpath.dirname(line.split(":", 1)[-1]) for line in embedded if not line.endswith("_test.go")}
    h = hashlib.sha256()
    for entry in tree.split(b"\0"):
        if not entry:
            continue
        _, path = entry.split(b"\t", 1)
        name = path.decode("utf-8")
        could_be_embedded = any(not root or name.startswith(root + "/") for root in roots)
        if relevant(name) or could_be_embedded:
            h.update(entry + b"\0")
    return h.hexdigest()


def api(path):
    return json.loads(command("gh", "api", path))


def trusted(run, repo, branch, run_id):
    if str(run["id"]) == run_id or (run.get("head_repository") or {}).get("full_name") != repo:
        return False
    if run["event"] == "push":
        return run["head_branch"] == "main"
    if run["event"] != "pull_request":
        return False
    if run["head_branch"] == branch:
        return True
    if branch == "main" and run.get("head_sha"):
        try:
            command("git", "merge-base", "--is-ancestor", run["head_sha"], "HEAD")
            return True
        except subprocess.CalledProcessError:
            return False
    return False


def package_sums(log):
    sums = {}
    for line in log.splitlines():
        line = re.sub(r"\x1b\[[0-9;]*m", "", line)
        m = re.search(r"(?:^|\s)([0-9a-f]{64})\s+\*?([^\r\n]+)$", line)
        if not m:
            continue
        name = m[2].strip().rsplit("/", 1)[-1]
        if not re.fullmatch(r"[A-Za-z0-9_.+~-]+", name):
            continue
        if not name.endswith((".deb", ".pkg", ".tar.gz")) or name.startswith(("kno-", "kno_")):
            continue
        if name in sums and sums[name] != m[1]:
            raise ValueError("conflicting package hashes: " + name)
        sums[name] = m[1]
    if not sums:
        raise ValueError("successful job log contains no package hashes")
    return "\n".join(sums[name] + "  " + name for name in sorted(sums))


def checked_out_commit(log):
    lines = log.splitlines()
    for i, line in enumerate(lines[:-1]):
        if "[command]" in line and re.search(r"git log -1 --format=%H$", line):
            candidate = re.sub(r"^\S+Z\s+", "", lines[i + 1]).strip()
            if re.fullmatch(r"[0-9a-f]{40}", candidate):
                return candidate
    raise ValueError("successful job log does not identify its checked-out commit")


def find(args):
    repo = os.environ["GITHUB_REPOSITORY"]
    branch = os.environ.get("GITHUB_HEAD_REF") or os.environ["GITHUB_REF_NAME"]
    run_id = os.environ["GITHUB_RUN_ID"]
    current = input_key("HEAD")
    runs = api(f"repos/{repo}/actions/workflows/{args.workflow}/runs?per_page=20")["workflow_runs"]
    for run in runs:
        if not trusted(run, repo, branch, run_id):
            continue
        created = datetime.fromisoformat(run["created_at"].replace("Z", "+00:00"))
        if datetime.now(timezone.utc) - created > timedelta(hours=24):
            continue
        jobs = api(f"repos/{repo}/actions/runs/{run['id']}/jobs?per_page=100")["jobs"]
        job = next((j for j in jobs if j["name"] == args.job and j["conclusion"] == "success"), None)
        if job is None:
            continue
        log = command("gh", "api", f"repos/{repo}/actions/jobs/{job['id']}/logs").decode()
        try:
            sha = checked_out_commit(log)
        except ValueError:
            continue
        try:
            command("git", "cat-file", "-e", sha + "^{commit}")
        except subprocess.CalledProcessError:
            command("git", "fetch", "--no-tags", "origin", sha)
        if input_key(sha) != current:
            continue
        sums = ""
        if args.sums:
            try:
                sums = package_sums(log)
            except ValueError:
                continue
        return job["html_url"], sha, sums
    return None


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--workflow", required=True)
    p.add_argument("--job", required=True)
    p.add_argument("--sums", action="store_true")
    args = p.parse_args()
    result = None
    automatic = os.environ.get("GITHUB_EVENT_NAME") in {"push", "pull_request"}
    if automatic and os.environ.get("GITHUB_RUN_ATTEMPT", "1") == "1" and not os.environ.get("ACT"):
        try:
            result = find(args)
        except (subprocess.SubprocessError, OSError, KeyError, ValueError, TypeError, UnicodeError) as e:
            print("CI reuse unavailable; running the job:", str(e), file=sys.stderr)
    with open(os.environ["GITHUB_OUTPUT"], "a") as out:
        print("reused=" + ("true" if result else "false"), file=out)
        if result:
            url, sha, sums = result
            print("source=" + sha, file=out)
            if sums:
                print("sums<<KNO_REUSED_SUMS\n" + sums + "\nKNO_REUSED_SUMS", file=out)
                print("Previously verified package hashes:\n" + sums)
            message = f"Reused {args.job} from {url}; its non-test source and workflow inputs match {sha}."
            print(message)
            if os.environ.get("GITHUB_STEP_SUMMARY"):
                with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
                    print(message, file=summary)
        else:
            print("No matching successful job; running all steps.")


if __name__ == "__main__":
    main()
