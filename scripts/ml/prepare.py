#!/usr/bin/env python3
"""Turn SWE-bench Multilingual tasks into Brokkr tasks. Runs in the Lima VM with
the system Python (standard library only).

    /usr/bin/python3 scripts/ml/prepare.py --repo gin-gonic/gin [--ids ID ...]

SWE-bench Multilingual (SWE-bench/SWE-bench_Multilingual on Hugging Face, 300
tasks in 9 languages) ships x86-64 Docker images. Brokkr's sandbox is an arm64
Firecracker microVM with no network, so each task gets its own environment
drive instead, built here at /opt/env and packed into ext4:

  Go   the official toolchain (at least the go.mod version), every module the
       repository needs (downloaded now; the guest runs GOPROXY=off,
       GOTOOLCHAIN=local) and a warm build cache for the dependencies, which
       the test command copies into the guest's tmpfs.

Task fields come from the dataset row unchanged: the base commit, the gold
patch (patches/good.patch), the hidden test patch (test.patch),
FAIL_TO_PASS + PASS_TO_PASS (required_tests) and the test command from the
row's eval_script. Grading is SWE-bench's: required tests only.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import sys
import urllib.request
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "gh"))
import prepare as gh  # noqa: E402  (clone, export, sh, sha256_file)

CACHE = Path(os.environ.get("BROKKR_ML_CACHE", Path.home() / ".cache/brokkr-ml"))
ENV_ROOT = gh.ENV_ROOT
DATASET = "SWE-bench/SWE-bench_Multilingual"

# Parser names map SWE-bench's log_parser to Brokkr's ports (internal/verify/multilang.go).
PARSERS = {"parse_log_gotest": "gotest", "parse_log_cargo": "cargo", "parse_log_googletest": "googletest",
           "parse_log_jq": "jq", "parse_log_redis": "redis", "parse_log_micropython_test": "micropython"}

GO_ENV = ("export PATH=/opt/env/{go}/bin:$PATH GOROOT=/opt/env/{go} GOMODCACHE=/opt/env/gomod GOPATH=/tmp/gopath "
          "GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-mod=mod GOCACHE=/tmp/gocache CGO_ENABLED=0; "
          # A tree of symlinks into the read-only warm cache: go reads the
          # entries and writes new ones beside them. Copying the cache instead
          # took 123 s per run on gin; this takes 3.5 s (NIGHTLOG).
          "cp -rs /opt/env/gocache /tmp/gocache 2>/dev/null; ")


def rows() -> list[dict]:
    cache = CACHE / "dataset.json"
    if cache.exists():
        return json.loads(cache.read_text())
    out = []
    for off in range(0, 400, 100):
        url = f"https://datasets-server.huggingface.co/rows?dataset={DATASET}&config=default&split=test&offset={off}&length=100"
        with urllib.request.urlopen(url, timeout=60) as r:
            out += [x["row"] for x in json.load(r)["rows"]]
    cache.parent.mkdir(parents=True, exist_ok=True)
    cache.write_text(json.dumps(out))
    return out


def test_command(eval_script: str) -> str:
    """The command between SWE-bench's start/end markers, without "| cat"."""
    s = eval_script.split(": '>>>>> Start Test Output'", 1)[1].split(": '>>>>> End Test Output'", 1)[0].strip()
    m = re.fullmatch(r"\((.*)\)\s*\|\s*cat", s, re.S)
    return (m.group(1) if m else s).strip()


def build_and_test_command(eval_script: str) -> str:
    """For repositories whose eval_script builds before testing (fmt): the
    commands after the hidden-test heredoc up to the end marker, without
    SWE-bench's markers and git bookkeeping. The verifier applies the hidden
    tests itself."""
    tail = eval_script.rsplit("EOF_114329324912", 1)[1].split(": '>>>>> End Test Output'", 1)[0]
    keep = []
    for line in tail.splitlines():
        line = line.strip()
        if not line or line.startswith((": '>>>>>", "git ")):
            continue
        m = re.fullmatch(r"\((.*)\)\s*\|\s*cat", line)
        keep.append(m.group(1) if m else line)
    return " && ".join(keep[:-1] + ["{ " + keep[-1] + "; }"]) if keep else ""


# C/C++ repositories whose eval_script carries the whole build.
CC_BUILDS_IN_SCRIPT = {"fmtlib/fmt"}
CC_ROOTFS = Path(os.environ.get("BROKKR_CACHE", Path.home() / ".cache/brokkr")) / "brokkr-rootfs-cc.ext4"


def go_version(repo_dir: Path) -> str:
    """The newest patch release of the go.mod minor version (at least 1.21)."""
    m = re.search(r"^go\s+(\d+)\.(\d+)", (repo_dir / "go.mod").read_text(), re.M)
    minor = max(int(m.group(2)) if m else 21, 21)
    with urllib.request.urlopen("https://go.dev/dl/?mode=json&include=all", timeout=60) as r:
        rel = json.load(r)
    cands = [x["version"] for x in rel if x["stable"] and re.fullmatch(rf"go1\.{minor}(\.\d+)?", x["version"])]
    if not cands:
        sys.exit(f"no stable go1.{minor} release")
    return max(cands, key=lambda v: [int(p) for p in v[2:].split(".")] + [0])


def build_go_env(repo: str, tasks: list[tuple[Path, str, str]]) -> Path:
    """One image for all of a repository's tasks: each Go version they need
    (at /opt/env/<version>), the union of their modules (the module cache is
    content-addressed, so commits share most of it) and a build cache warmed
    for every task's test packages. tasks: (repo dir, Go version, packages)."""
    vers = sorted({v for _, v, _ in tasks})
    h = gh.hashlib.sha256()
    for d, v, pk in sorted(tasks):
        gosum = d / "go.sum"
        h.update(f"{d.parent.name} {v} {pk} {gh.sha256_file(gosum) if gosum.exists() else ''}\n".encode())
    name = f"{repo.replace('/', '__')}-{h.hexdigest()[:12]}"
    image = CACHE / "envs" / f"{name}.ext4"
    if image.exists():
        return image
    image.parent.mkdir(parents=True, exist_ok=True)
    # Go makes its module cache read-only; make it writable to clear it.
    gh.sh("chmod", "-R", "u+w", str(ENV_ROOT))
    for child in ENV_ROOT.iterdir():
        shutil.rmtree(child) if child.is_dir() and not child.is_symlink() else child.unlink()
    for ver in vers:
        tgz = CACHE / "go" / f"{ver}.linux-arm64.tar.gz"
        if not tgz.exists():
            tgz.parent.mkdir(parents=True, exist_ok=True)
            print(f"download {ver}", flush=True)
            urllib.request.urlretrieve(f"https://go.dev/dl/{ver}.linux-arm64.tar.gz", tgz)
        gh.sh("tar", "-xzf", str(tgz), "-C", str(ENV_ROOT))
        os.rename(ENV_ROOT / "go", ENV_ROOT / ver)
        # Not needed to build or test: the toolchain's own test suite and docs.
        for extra in ("test", "doc", "api", "misc"):
            shutil.rmtree(ENV_ROOT / ver / extra, ignore_errors=True)
    for d, ver, pkgs in tasks:
        env = {"PATH": f"{ENV_ROOT}/{ver}/bin:{os.environ['PATH']}", "GOROOT": str(ENV_ROOT / ver),
               "GOMODCACHE": str(ENV_ROOT / "gomod"), "GOCACHE": str(ENV_ROOT / "gocache"), "GOPATH": str(CACHE / "gopath"),
               "GOTOOLCHAIN": "local", "GOFLAGS": "-mod=mod", "CGO_ENABLED": "0"}
        r = gh.sh("go", "mod", "download", cwd=d, env=env, check=False)
        if r.returncode != 0:
            sys.exit(f"{d.parent.name}: go mod download failed:\n{r.stderr[-1500:]}")
        # Compile the test binaries once so dependencies are in the build
        # cache. -c compiles without running: repository code never executes
        # outside a microVM.
        w = CACHE / "build" / d.parent.name
        shutil.rmtree(w, ignore_errors=True)
        shutil.copytree(d, w)
        bins = CACHE / "build" / f"{d.parent.name}.bin"
        bins.mkdir(parents=True, exist_ok=True)
        r = gh.sh("go", "test", "-c", "-o", str(bins) + "/", *pkgs.split(), cwd=w, env=env, check=False)
        print(f"{d.parent.name}: warm-up compile exit {r.returncode}", flush=True)
        shutil.rmtree(w, ignore_errors=True)
        shutil.rmtree(bins, ignore_errors=True)
    gh.sh("chmod", "-R", "a+rX", str(ENV_ROOT))
    manifest = {"repo": repo, "go": vers, "tasks": [d.parent.name for d, _, _ in tasks],
                "modules": len(list((ENV_ROOT / "gomod" / "cache" / "download").rglob("*.zip")))}
    (ENV_ROOT / "brokkr-env.json").write_text(json.dumps(manifest, indent=2))
    size = int(gh.sh("du", "-sm", str(ENV_ROOT)).stdout.split()[0]) * 11 // 10 + 128
    gh.sh("mkfs.ext4", "-q", "-F", "-d", str(ENV_ROOT), str(image) + ".tmp", f"{size}M")
    os.replace(str(image) + ".tmp", image)
    Path(str(image) + ".sha256").write_text(gh.sha256_file(image) + "\n")
    # The image is the product; the staging copy would only take disk space.
    gh.sh("chmod", "-R", "u+w", str(ENV_ROOT))
    for child in ENV_ROOT.iterdir():
        shutil.rmtree(child) if child.is_dir() and not child.is_symlink() else child.unlink()
    print(f"env {name}: {', '.join(vers)}, {manifest['modules']} modules, {size} MiB", flush=True)
    return image


def main() -> None:
    if Path(sys.executable).resolve().is_relative_to(ENV_ROOT):
        sys.exit(f"refusing to run from {sys.executable}: building an env empties {ENV_ROOT}")
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo", required=True)
    ap.add_argument("--ids", nargs="*")
    args = ap.parse_args()
    todo = [r for r in rows() if r["repo"] == args.repo and (not args.ids or r["instance_id"] in args.ids)]
    if not todo:
        sys.exit(f"no tasks for {args.repo}")
    rd = gh.src(args.repo)
    if args.repo in CC_BUILDS_IN_SCRIPT:
        return prepare_cc(args.repo, rd, todo)
    ready = []
    for row in todo:
        iid = row["instance_id"]
        if PARSERS.get(row["log_parser"]) != "gotest":
            print(f"skip {iid}: {row['log_parser']} environments are not built yet", flush=True)
            continue
        cmd = test_command(row["eval_script"])
        m = re.search(r"go test\s+(?:-\S+\s+)*((?:\.\S*\s*)+)", cmd)
        if not m:
            print(f"skip {iid}: no package list in {cmd!r}", flush=True)
            continue
        d = CACHE / "tasks" / iid
        gh.sh("git", "-C", str(rd), "fetch", "--quiet", "origin", row["base_commit"], check=False)
        gh.export(rd, row["base_commit"], d / "repo")
        ready.append((row, d, cmd, go_version(d / "repo"), m.group(1).split()))
    image = build_go_env(args.repo, [(d / "repo", ver, " ".join(pk)) for _, d, _, ver, pk in ready])
    for row, d, cmd, ver, _ in ready:
        iid = row["instance_id"]
        (d / "patches").mkdir(parents=True, exist_ok=True)
        (d / "test.patch").write_text(row["test_patch"])
        (d / "patches/good.patch").write_text(row["patch"])
        f2p = json.loads(row["FAIL_TO_PASS"]) if isinstance(row["FAIL_TO_PASS"], str) else row["FAIL_TO_PASS"]
        p2p = json.loads(row["PASS_TO_PASS"]) if isinstance(row["PASS_TO_PASS"], str) else row["PASS_TO_PASS"]
        test_files = sorted({l.split(" b/")[-1] for l in row["test_patch"].splitlines() if l.startswith("diff --git ")})
        task = {
            "name": iid,
            "issue": row["problem_statement"],
            "test_cmd": GO_ENV.format(go=ver) + cmd,
            "timeout_s": 1200,
            "protect": test_files,
            "required_tests": f2p + p2p,
            "test_patch": "test.patch",
            "parser": "gotest",
            "pass_rule": "required_only",
            "env_image": os.path.relpath(image, d),
            "mem_mib": 2048,
            "swebench": {"dataset": DATASET, "fail_to_pass": len(f2p), "pass_to_pass": len(p2p),
                         "base_commit": row["base_commit"], "image": row["image"], "go": ver},
        }
        (d / "task.json").write_text(json.dumps(task, indent=2) + "\n")
        print(f"{iid}: F2P {len(f2p)} P2P {len(p2p)} {ver} cmd {cmd}", flush=True)


def prepare_cc(repo: str, rd: Path, todo: list[dict]) -> None:
    """C/C++ tasks: no env drive; the guest root image with the toolchain
    (scripts/build-rootfs-cc.sh), and the build runs inside the microVM."""
    if not CC_ROOTFS.exists():
        sys.exit(f"missing {CC_ROOTFS}; run scripts/build-rootfs-cc.sh")
    for row in todo:
        iid = row["instance_id"]
        parser = PARSERS.get(row["log_parser"])
        cmd = build_and_test_command(row["eval_script"])
        if not parser or not cmd:
            print(f"skip {iid}: parser {row['log_parser']}, command {cmd!r}", flush=True)
            continue
        d = CACHE / "tasks" / iid
        gh.sh("git", "-C", str(rd), "fetch", "--quiet", "origin", row["base_commit"], check=False)
        gh.export(rd, row["base_commit"], d / "repo")
        (d / "patches").mkdir(parents=True, exist_ok=True)
        (d / "test.patch").write_text(row["test_patch"])
        (d / "patches/good.patch").write_text(row["patch"])
        f2p = json.loads(row["FAIL_TO_PASS"]) if isinstance(row["FAIL_TO_PASS"], str) else row["FAIL_TO_PASS"]
        p2p = json.loads(row["PASS_TO_PASS"]) if isinstance(row["PASS_TO_PASS"], str) else row["PASS_TO_PASS"]
        test_files = sorted({l.split(" b/")[-1] for l in row["test_patch"].splitlines() if l.startswith("diff --git ")})
        task = {
            "name": iid, "issue": row["problem_statement"],
            # $(nproc) is the guest's 1 vCPU; build output stays in /work.
            "test_cmd": cmd, "timeout_s": 2400,
            "protect": test_files, "required_tests": f2p + p2p, "test_patch": "test.patch",
            "parser": parser, "pass_rule": "required_only",
            "rootfs": os.path.relpath(CC_ROOTFS, d), "mem_mib": 2048,
            "swebench": {"dataset": DATASET, "fail_to_pass": len(f2p), "pass_to_pass": len(p2p),
                         "base_commit": row["base_commit"], "image": row["image"]},
        }
        (d / "task.json").write_text(json.dumps(task, indent=2) + "\n")
        print(f"{iid}: F2P {len(f2p)} P2P {len(p2p)} cmd {cmd}", flush=True)


if __name__ == "__main__":
    main()
