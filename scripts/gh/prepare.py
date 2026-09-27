#!/usr/bin/env python3
"""Turn fixed GitHub issues into Brokkr tasks, the SWE-bench way. Runs in the Lima VM.

    /usr/bin/python3 scripts/gh/prepare.py --candidates results/pydantic/candidates.json

Standard library only. Run it with the system Python, never an interpreter
under /opt/env: building an environment empties /opt/env.

The candidates file (written on the Mac with `gh`, see NIGHTLOG) lists issues
fixed by exactly one merged PR. For each:

- base commit  = first parent of the PR's merge commit (main just before the fix)
- test.patch   = the PR's changes under the repo's test directory: the hidden tests
- good.patch   = the PR's other changes: the reference fix, for validity only
- repo/        = the tree at the base commit, without .git
- env image    = the repo's locked dependencies at the base commit (uv.lock),
                 one image per distinct lock file, mounted at /opt/env

required_tests are left empty here. scripts/gh/derive_tests.sh fills them the
way SWE-bench's harness does: tests failing before the fix and passing after
it are FAIL_TO_PASS; tests passing both times are PASS_TO_PASS.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

CACHE = Path(os.environ.get("BROKKR_GH_CACHE", Path.home() / ".cache/brokkr-gh"))
ENV_ROOT = Path("/opt/env")  # must match the guest mount point
UV = shutil.which("uv") or str(Path.home() / ".local/bin/uv")

REPOS = {
    "pydantic/pydantic": {
        "python": "3.12",
        "tests_dir": "tests/",
        # uv export of the locked "dev" group: pytest and the plugins pydantic's
        # own test suite uses, with pydantic-core pinned by the lock.
        "uv_groups": ["dev"],
        # -rA gives the summary lines the parser reads. pytest-pretty rewrites
        # that summary, so it is switched off; nothing else in the suite needs it.
        "test_cmd": "PYTHONPATH=$PWD python -m pytest -rA -p no:cacheprovider -p no:pretty {files}",
        "parser": "pytest",
        "pass_rule": "required_only",
        # pydantic-core lives in the repo (Rust) and is compiled from the base
        # commit's own source: the Python code often needs core functions no
        # release has yet (tried first with PyPI wheels; 13780 failed to import
        # _schema_gather). One env per distinct core tree; builds share one
        # cargo target dir, so later builds recompile only what changed.
        "workspace_core": {"path": "pydantic-core", "package": "pydantic-core"},
        # Fixes that change the Rust core are excluded: verifying them would
        # mean compiling Rust in the sandbox for every test run.
        "exclude_fix_paths": ["pydantic-core/"],
        # Live mode's regression set: pydantic's own tests. tests/ also holds
        # the core's tests (they need hypothesis, not in the dev group), mypy
        # and benchmark suites; collecting all of it stops at an import error
        # and runs nothing (found on the first live baseline, #13754).
        "live_regression": "tests/test_*.py",
    },
}


def core_tree(cfg: dict, repo_dir: Path, base: str) -> str | None:
    """The git tree id of the in-repo core at base: identifies what gets compiled."""
    wc = cfg.get("workspace_core")
    return sh("git", "-C", str(repo_dir), "rev-parse", f"{base}:{wc['path']}").stdout.strip() if wc else None


def sh(*args, cwd=None, check=True, env=None) -> subprocess.CompletedProcess:
    return subprocess.run(args, cwd=cwd, check=check, text=True, capture_output=True,
                          env={**os.environ, **(env or {})})


def sha256_file(p: Path) -> str:
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def src(repo: str) -> Path:
    d = CACHE / "src" / repo.replace("/", "__")
    if not d.exists():
        d.parent.mkdir(parents=True, exist_ok=True)
        print(f"clone {repo}", flush=True)
        for attempt in range(3):  # a transient network error must not end the run
            r = sh("git", "clone", "--quiet", f"https://github.com/{repo}.git", str(d), check=False)
            if r.returncode == 0:
                break
            shutil.rmtree(d, ignore_errors=True)
        else:
            sys.exit(f"clone {repo} failed:\n{r.stderr[-1000:]}")
    return d


def export(repo_dir: Path, commit: str, dest: Path) -> None:
    if dest.exists():
        shutil.rmtree(dest)
    dest.mkdir(parents=True)
    a = subprocess.run(["git", "-C", str(repo_dir), "archive", commit], check=True, capture_output=True)
    subprocess.run(["tar", "-x", "-C", str(dest)], input=a.stdout, check=True)


def build_env(repo: str, cfg: dict, tree: Path, core: str | None) -> Path:
    lock = tree / "uv.lock"
    cv = core  # the core's git tree id, or None
    key = hashlib.sha256((sha256_file(lock) + (cv or "")).encode()).hexdigest()[:12]
    name = f"{repo.replace('/', '__')}-{key}"
    image = CACHE / "envs" / f"{name}.ext4"
    if image.exists():
        return image
    image.parent.mkdir(parents=True, exist_ok=True)
    print(f"env {name}: python {cfg['python']}", flush=True)
    for child in ENV_ROOT.iterdir():
        shutil.rmtree(child) if child.is_dir() and not child.is_symlink() else child.unlink()
    pyenv = {"UV_PYTHON_INSTALL_DIR": str(ENV_ROOT / "python"), "UV_CACHE_DIR": str(CACHE / "uv-cache")}
    sh(UV, "python", "install", cfg["python"], env=pyenv)
    sh(UV, "venv", "--allow-existing", "--python", cfg["python"], "--python-preference", "only-managed", str(ENV_ROOT), env=pyenv)
    reqs = CACHE / "build" / f"{name}.txt"
    reqs.parent.mkdir(parents=True, exist_ok=True)
    groups = [a for g in cfg["uv_groups"] for a in ("--group", g)]
    r = sh(UV, "export", "--frozen", "--no-hashes", "--no-emit-project", "--no-header", *groups, "-o", str(reqs), cwd=tree, check=False, env=pyenv)
    if r.returncode != 0:
        sys.exit(f"{name}: uv export failed:\n{r.stderr[-2000:]}")
    if cv:
        # Build the in-repo core from this tree (not editable: the guest sees the
        # repo at another path). cwd=tree makes "./pydantic-core" resolve here.
        wc = cfg["workspace_core"]
        reqs.write_text("\n".join(("./" + wc["path"]) if l.strip() == f"-e ./{wc['path']}" else l
                                  for l in reqs.read_text().splitlines()) + "\n")
        pyenv = {**pyenv, "CARGO_TARGET_DIR": str(CACHE / "cargo-target"),
                 "PATH": f"{Path.home() / '.cargo/bin'}:{os.environ.get('PATH', '')}"}
    r = sh(UV, "pip", "install", "--python", str(ENV_ROOT / "bin/python"), "-r", str(reqs), check=False, env=pyenv, cwd=tree)
    if r.returncode != 0:
        sys.exit(f"{name}: installing locked deps failed:\n{r.stderr[-2000:]}")
    probe = sh(str(ENV_ROOT / "bin/python3"), "-c", "import sys, pytest; print(sys.prefix)", check=False)
    if probe.stdout.strip() != str(ENV_ROOT):
        sys.exit(f"{name}: /opt/env/bin/python3 does not use the venv or lacks pytest: {probe.stdout!r} {probe.stderr[-300:]!r}")
    freeze = sh(UV, "pip", "freeze", "--python", str(ENV_ROOT / "bin/python"), env=pyenv).stdout
    manifest = {"repo": repo, "uv_lock_sha256": sha256_file(lock), "core_tree": cv,
                "core_note": "pydantic-core compiled from the base commit's own source" if cv else None,
                "python": sh(str(ENV_ROOT / "bin/python"), "-V").stdout.strip(),
                "groups": cfg["uv_groups"], "installed": freeze.splitlines(),
                "note": "the repository itself is not installed; the working copy is imported via PYTHONPATH"}
    (ENV_ROOT / "brokkr-env.json").write_text(json.dumps(manifest, indent=2))
    size = int(sh("du", "-sm", str(ENV_ROOT)).stdout.split()[0]) + 256
    sh("mkfs.ext4", "-q", "-F", "-d", str(ENV_ROOT), str(image) + ".tmp", f"{size}M")
    os.replace(str(image) + ".tmp", image)
    Path(str(image) + ".sha256").write_text(sha256_file(image) + "\n")
    (image.parent / f"{name}.manifest.json").write_text(json.dumps(manifest, indent=2))
    print(f"env {name}: {manifest['python']}, {len(manifest['installed'])} packages", flush=True)
    return image


def main() -> None:
    if Path(sys.executable).resolve().is_relative_to(ENV_ROOT):
        sys.exit(f"refusing to run from {sys.executable}: building an env empties {ENV_ROOT}")
    ap = argparse.ArgumentParser()
    ap.add_argument("--candidates", required=True)
    ap.add_argument("--ids", nargs="*")
    args = ap.parse_args()
    c = json.load(open(args.candidates))
    repo, cfg = c["repo"], REPOS[c["repo"]]
    rd = src(repo)
    sh("git", "-C", str(rd), "fetch", "--quiet", "origin")
    done = 0
    for cand in c["candidates"]:
        if args.ids and cand["instance_id"] not in args.ids:
            continue
        merge = cand["merge_commit"]
        parents = sh("git", "-C", str(rd), "rev-list", "--parents", "-n", "1", merge).stdout.split()[1:]
        base = parents[0]
        td = cfg["tests_dir"]
        test_patch = sh("git", "-C", str(rd), "diff", base, merge, "--", td).stdout
        gold = sh("git", "-C", str(rd), "diff", base, merge, "--", ".", f":!{td}").stdout
        if not test_patch.strip() or not gold.strip():
            print(f"skip {cand['instance_id']}: empty test or code diff", flush=True)
            continue
        changed = sh("git", "-C", str(rd), "diff", "--name-only", base, merge).stdout.split()
        if any(f.startswith(p) for f in changed for p in cfg.get("exclude_fix_paths", [])):
            print(f"skip {cand['instance_id']}: fix changes {cfg['exclude_fix_paths']}", flush=True)
            continue
        d = CACHE / "tasks" / cand["instance_id"]
        export(rd, base, d / "repo")
        image = build_env(repo, cfg, d / "repo", core_tree(cfg, rd, base))
        (d / "patches").mkdir(parents=True, exist_ok=True)
        (d / "test.patch").write_text(test_patch)
        (d / "patches/good.patch").write_text(gold)
        files = sorted({l.split(" b/")[-1] for l in test_patch.splitlines() if l.startswith("diff --git ")})
        # pytest targets: test_*.py files the PR touched (conftest.py and data
        # files are protected but not run directly).
        test_files = [f for f in files if f.endswith(".py") and f.split("/")[-1].startswith("test_")]
        if not test_files:
            print(f"skip {cand['instance_id']}: PR changes no test_*.py file", flush=True)
            continue
        task = {
            "name": cand["instance_id"],
            "issue": f"{cand['issue_title']}\n\n{cand['issue_body']}",
            "test_cmd": cfg["test_cmd"].format(files=" ".join(test_files)),
            "timeout_s": 900,
            "protect": files,
            "required_tests": [],
            "test_patch": "test.patch",
            "parser": cfg["parser"],
            "pass_rule": cfg["pass_rule"],
            "env_image": os.path.relpath(image, d),
            "mem_mib": 1024,
            "source": {"repo": repo, "issue": cand["issue"], "pr": cand["pr"], "merge_commit": merge,
                       "base_commit": base, "merged_at": cand["merged_at"]},
        }
        (d / "task.json").write_text(json.dumps(task, indent=2) + "\n")
        done += 1
    print(f"wrote {done} tasks under {CACHE / 'tasks'}", flush=True)


if __name__ == "__main__":
    main()
