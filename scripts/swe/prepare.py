#!/usr/bin/env python3
"""Turn SWE-bench Verified instances into Brokkr tasks. Runs inside the Lima VM.

    uv run --with pyarrow scripts/swe/prepare.py --repo django/django --versions 4.0 4.1 4.2
    uv run --with pyarrow scripts/swe/prepare.py --repo sympy/sympy --versions all

For each (repo, version) it builds one environment image, and for each instance
one task directory:

    ~/.cache/brokkr-swe/envs/<repo>-<version>.ext4   interpreter + dependencies,
                                                     built at /opt/env, mounted
                                                     read-only at /opt/env in the guest
    ~/.cache/brokkr-swe/tasks/<instance_id>/
        task.json            Brokkr task: issue text, test command, required tests
        repo/                the repository at base_commit, without .git
        test.patch           SWE-bench's test patch, applied only by the verifier
        patches/good.patch   SWE-bench's gold patch, for the validity check only

The environment follows SWE-bench v4.1.0's MAP_REPO_VERSION_TO_SPECS (python
version, requirements file, install step), with two deliberate differences,
recorded in each env's manifest.json:

- Python comes from uv's standalone builds, not conda, because the guest is
  arm64 and has no conda.
- The repository itself is not left installed. SWE-bench installs it editable at
  /testbed; here its dependencies are installed, the package is uninstalled, and
  the test command puts the working copy first on PYTHONPATH. A candidate patch
  is therefore always what gets imported.

Requirements that fail to build on arm64 are skipped one by one and listed in
the manifest; tests needing them are expected to skip. Tasks whose tests then
cannot pass are caught by the validity check (gold patch must PASS, no patch
must FAIL), not assumed away.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

import pyarrow.parquet as pq

CACHE = Path(os.environ.get("BROKKR_SWE_CACHE", Path.home() / ".cache/brokkr-swe"))
ENV_ROOT = Path("/opt/env")  # must match the guest mount point
UV = shutil.which("uv") or str(Path.home() / ".local/bin/uv")

# Per repository, from SWE-bench v4.1.0 swebench/harness/constants/python.py
# (SPECS_*, TEST_*, MAP_REPO_TO_REQS_PATHS) and its get_test_directives.
def _django_labels(files: list[str]) -> str:
    # tests/a/b.py -> a.b
    out = []
    for f in files:
        if f.endswith(".py"):
            f = f[:-3]
            f = f[len("tests/"):] if f.startswith("tests/") else f
            out.append(f.replace("/", "."))
    return " ".join(out)


REPOS = {
    "django/django": {
        "python": {"4.0": "3.8", "4.1": "3.9", "4.2": "3.9"},
        "reqs_file": "tests/requirements/py3.txt",
        "pip": [],
        "package": "Django",
        # --parallel 1 as in SWE-bench; PYTHONPATH makes the working copy the
        # Django that is imported (see module docstring).
        "test_cmd": "PYTHONPATH=$PWD python3 tests/runtests.py --verbosity 2 --settings=test_sqlite --parallel 1 {labels}",
        "labels": _django_labels,
        "parser": "django",
    },
    "sympy/sympy": {
        "python": {},  # every version: 3.9
        "default_python": "3.9",
        "reqs_file": None,  # SPECS_SYMPY "packages": "mpmath flake8" plus pip_packages
        "pip": ["mpmath==1.3.0", "flake8", "flake8-comprehensions"],
        "package": "sympy",
        "test_cmd": "PYTHONPATH=$PWD PYTHONWARNINGS='ignore::UserWarning,ignore::SyntaxWarning' bin/test -C --verbose {labels}",
        "labels": lambda files: " ".join(f for f in files if f.endswith(".py")),
        "parser": "sympy",
        # SWE-bench's criterion: required tests pass, exit code ignored. Old
        # releases have tests that error on Python 3.9 even with the gold patch,
        # which SWE-bench leaves out of FAIL_TO_PASS/PASS_TO_PASS.
        "pass_rule": "required_only",
    },
}


def python_for(repo: str, version: str) -> str:
    c = REPOS[repo]
    return c["python"].get(version) or c["default_python"]


def sh(*args: str, cwd: Path | None = None, check: bool = True, env: dict | None = None) -> subprocess.CompletedProcess:
    return subprocess.run(args, cwd=cwd, check=check, text=True, capture_output=True,
                          env={**os.environ, **(env or {})})


def src_checkout(repo: str) -> Path:
    src = CACHE / "src" / repo.replace("/", "__")
    if not src.exists():
        src.parent.mkdir(parents=True, exist_ok=True)
        print(f"clone {repo}", flush=True)
        sh("git", "clone", "--quiet", f"https://github.com/{repo}.git", str(src))
    return src


def export(src: Path, commit: str, dest: Path) -> None:
    """The tree at commit, without .git."""
    if dest.exists():
        shutil.rmtree(dest)
    dest.mkdir(parents=True)
    archive = subprocess.run(["git", "-C", str(src), "archive", commit], check=True, capture_output=True)
    subprocess.run(["tar", "-x", "-C", str(dest)], input=archive.stdout, check=True)


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def build_env(repo: str, version: str, setup_commit: str) -> Path:
    name = f"{repo.replace('/', '__')}-{version}"
    image = CACHE / "envs" / f"{name}.ext4"
    if image.exists():
        return image
    image.parent.mkdir(parents=True, exist_ok=True)
    cfg = REPOS[repo]
    spec = {"python": python_for(repo, version)}
    print(f"env {name}: python {spec['python']}", flush=True)

    for child in ENV_ROOT.iterdir():  # /opt/env is owned by us, created once
        shutil.rmtree(child) if child.is_dir() and not child.is_symlink() else child.unlink()
    pyenv = {"UV_PYTHON_INSTALL_DIR": str(ENV_ROOT / "python"), "UV_CACHE_DIR": str(CACHE / "uv-cache")}
    sh(UV, "python", "install", spec["python"], env=pyenv)
    # The venv is /opt/env itself, so /opt/env/bin (on the guest's PATH) is the
    # venv's real bin directory. A symlinked bin does not work: Python finds
    # pyvenv.cfg from the executable's path as written, and through the link it
    # looks in the wrong directory and silently runs without the venv.
    sh(UV, "venv", "--allow-existing", "--python", spec["python"], "--python-preference", "only-managed",
       str(ENV_ROOT), env=pyenv)
    py = str(ENV_ROOT / "bin/python")

    work = CACHE / "build" / name
    export(src_checkout(repo), setup_commit, work)
    reqs = list(cfg["pip"])
    if cfg["reqs_file"]:
        reqs += [l.strip() for l in (work / cfg["reqs_file"]).read_text().splitlines()
                 if l.strip() and not l.strip().startswith("#")]
    skipped: list[dict] = []
    install = lambda *pkgs: sh(UV, "pip", "install", "--python", py, *pkgs, check=False, env=pyenv, cwd=work)
    r = install(*reqs)
    if r.returncode != 0:  # retry one by one, keep what builds
        for req in reqs:
            rr = install(req)
            if rr.returncode != 0:
                skipped.append({"requirement": req, "error": rr.stderr.strip().splitlines()[-1:]})
    # The package's own install_requires, then drop the package itself.
    r = install(".")
    if r.returncode != 0:
        sys.exit(f"{name}: installing {repo} failed:\n{r.stderr[-2000:]}")
    sh(UV, "pip", "uninstall", "--python", py, cfg["package"], env=pyenv)
    shutil.rmtree(work)

    # Check what the guest will do: run python3 from /opt/env/bin and import a
    # dependency. A venv the interpreter does not pick up fails here, not later.
    probe = sh(str(ENV_ROOT / "bin/python3"), "-c", "import sys; print(sys.prefix)", check=False)
    if probe.stdout.strip() != str(ENV_ROOT):
        sys.exit(f"{name}: /opt/env/bin/python3 does not use the venv (prefix {probe.stdout.strip()!r})")
    freeze = sh(UV, "pip", "freeze", "--python", py, env=pyenv).stdout
    pyver = sh(py, "-V").stdout.strip() or sh(py, "-V").stderr.strip()
    manifest = {
        "repo": repo, "version": version, "environment_setup_commit": setup_commit,
        "python": pyver, "swebench_spec_python": spec["python"],
        "requirements_file": cfg["reqs_file"], "pip_packages": cfg["pip"], "skipped_requirements": skipped,
        "installed": freeze.splitlines(),
        "differences_from_swebench": [
            "python from uv standalone builds (arm64), not conda",
            f"{cfg['package']} not installed; working copy imported via PYTHONPATH",
        ],
    }
    (ENV_ROOT / "brokkr-env.json").write_text(json.dumps(manifest, indent=2))
    size = int(sh("du", "-sm", str(ENV_ROOT)).stdout.split()[0]) + 256
    sh("mkfs.ext4", "-q", "-F", "-d", str(ENV_ROOT), str(image) + ".tmp", f"{size}M")
    os.replace(str(image) + ".tmp", image)
    Path(str(image) + ".sha256").write_text(sha256(image) + "\n")
    (image.parent / f"{name}.manifest.json").write_text(json.dumps(manifest, indent=2))
    print(f"env {name}: {pyver}, {len(freeze.splitlines())} packages, {len(skipped)} skipped", flush=True)
    return image


def test_files(test_patch: str) -> list[str]:
    return sorted({m for m in re.findall(r"^diff --git a/(\S+) b/", test_patch, re.M)})


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--dataset", default=str(CACHE / "verified.parquet"))
    ap.add_argument("--repo", required=True)
    ap.add_argument("--versions", nargs="+", required=True)
    ap.add_argument("--ids", nargs="*", help="only these instance ids")
    args = ap.parse_args()

    rows = [r for r in pq.read_table(args.dataset).to_pylist()
            if r["repo"] == args.repo and ("all" in args.versions or r["version"] in args.versions)
            and (not args.ids or r["instance_id"] in args.ids)]
    print(f"{len(rows)} instances", flush=True)
    src = src_checkout(args.repo)
    envs = {}
    for r in rows:
        key = r["version"]
        if key not in envs:
            envs[key] = build_env(args.repo, key, r["environment_setup_commit"])

    for r in rows:
        d = CACHE / "tasks" / r["instance_id"]
        export(src, r["base_commit"], d / "repo")
        (d / "patches").mkdir(parents=True, exist_ok=True)
        (d / "test.patch").write_text(r["test_patch"])
        (d / "patches/good.patch").write_text(r["patch"])
        files = test_files(r["test_patch"])
        task = {
            "name": r["instance_id"],
            "issue": r["problem_statement"],
            "test_cmd": REPOS[args.repo]["test_cmd"].format(labels=REPOS[args.repo]["labels"](files)),
            "timeout_s": 900,
            "protect": files,
            "required_tests": json.loads(r["FAIL_TO_PASS"]) + json.loads(r["PASS_TO_PASS"]),
            "test_patch": "test.patch",
            "parser": REPOS[args.repo]["parser"],
            **({"pass_rule": REPOS[args.repo]["pass_rule"]} if "pass_rule" in REPOS[args.repo] else {}),
            "env_image": os.path.relpath(envs[r["version"]], d),
            "mem_mib": 1024,
            "swebench": {
                "dataset": "princeton-nlp/SWE-bench_Verified", "version": r["version"],
                "difficulty": r["difficulty"], "base_commit": r["base_commit"],
                "fail_to_pass": len(json.loads(r["FAIL_TO_PASS"])),
                "pass_to_pass": len(json.loads(r["PASS_TO_PASS"])),
            },
        }
        (d / "task.json").write_text(json.dumps(task, indent=2) + "\n")
    print(f"wrote {len(rows)} tasks under {CACHE / 'tasks'}", flush=True)


if __name__ == "__main__":
    main()
