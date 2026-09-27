#!/usr/bin/env python3
"""Build a live-mode task: the agent fixes an issue AND writes the regression
test that proves it. Runs in the Lima VM with the system Python.

    /usr/bin/python3 scripts/gh/live_task.py issue pydantic/pydantic 12345
    /usr/bin/python3 scripts/gh/live_task.py variant TASK_DIR [TASK_DIR ...]

issue    an open GitHub issue, at the default branch's current HEAD. There are
         no hidden tests: `brokkr fix` judges the run by the agent's own test
         (verify.SelfTest: it must fail on the original code, pass with the
         fix, and no existing test in test_cmd may break). Written to
         ~/.cache/brokkr-gh/live/<owner>__<repo>-<N>/.

variant  a live copy (task.live.json) of a historical task built by
         prepare.py. It keeps the hidden tests, so a run records both
         verdicts: does the agent's own test agree with the maintainers'?

Nothing is posted anywhere. The GitHub API is read without a token (60
requests an hour is plenty for one issue at a time).
"""
from __future__ import annotations

import json
import sys
import urllib.request
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import prepare  # noqa: E402  (same directory; standard library only)


def fetch_issue(repo: str, n: int) -> dict:
    req = urllib.request.Request(f"https://api.github.com/repos/{repo}/issues/{n}",
                                 headers={"Accept": "application/vnd.github+json", "User-Agent": "brokkr"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


def live_fields(cfg: dict) -> dict:
    td = cfg["tests_dir"]
    return {"live": True, "new_tests_dir": td, "protect": [td]}


def from_issue(repo: str, n: int) -> Path:
    cfg = prepare.REPOS[repo]
    issue = fetch_issue(repo, n)
    if "pull_request" in issue:
        sys.exit(f"{repo}#{n} is a pull request, not an issue")
    if issue["state"] != "open":
        print(f"note: {repo}#{n} is {issue['state']}", flush=True)
    rd = prepare.src(repo)
    prepare.sh("git", "-C", str(rd), "fetch", "--quiet", "origin")
    head = prepare.sh("git", "-C", str(rd), "rev-parse", "origin/HEAD").stdout.strip()
    d = prepare.CACHE / "live" / f"{repo.replace('/', '__')}-{n}"
    prepare.export(rd, head, d / "repo")
    image = build_env_or_exit(repo, cfg, d, rd, head)
    task = {
        "name": f"{repo.replace('/', '__')}-{n}",
        "issue": f"{issue['title']}\n\n{issue.get('body') or ''}",
        # The regression set: the repository's own test suite (see
        # live_regression in prepare.py), not only the affected files:
        # "breaks nothing" should mean it.
        "test_cmd": cfg["test_cmd"].format(files=cfg.get("live_regression", cfg["tests_dir"])),
        "timeout_s": 1800,
        "required_tests": [],
        "parser": cfg["parser"],
        "env_image": prepare.os.path.relpath(image, d),
        "mem_mib": 2048,
        **live_fields(cfg),
        "source": {"repo": repo, "issue": n, "url": issue["html_url"], "base_commit": head, "state": issue["state"]},
    }
    (d / "task.json").write_text(json.dumps(task, indent=2) + "\n")
    print(f"live task {task['name']} at {head[:12]}: {d / 'task.json'}", flush=True)
    return d


def build_env_or_exit(repo: str, cfg: dict, d: Path, rd: Path, head: str) -> Path:
    if Path(sys.executable).resolve().is_relative_to(prepare.ENV_ROOT):
        sys.exit(f"refusing to run from {sys.executable}: building an env empties {prepare.ENV_ROOT}")
    return prepare.build_env(repo, cfg, d / "repo", prepare.core_tree(cfg, rd, head))


def variant(task_dir: Path) -> Path:
    t = json.loads((task_dir / "task.json").read_text())
    cfg = prepare.REPOS[t["source"]["repo"]]
    if not t.get("required_tests"):
        sys.exit(f"{task_dir}: no required_tests; run derive_tests.py first")
    live = {**t, **live_fields(cfg), "name": t["name"] + "-live"}
    out = task_dir / "task.live.json"
    out.write_text(json.dumps(live, indent=2) + "\n")
    print(f"{out}", flush=True)
    return out


def main() -> None:
    if len(sys.argv) >= 4 and sys.argv[1] == "issue":
        from_issue(sys.argv[2], int(sys.argv[3].lstrip("#")))
    elif len(sys.argv) >= 3 and sys.argv[1] == "variant":
        for d in sys.argv[2:]:
            variant(Path(d))
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main()
