//! brokkr-runner: run one command against one repository inside a fresh
//! Firecracker microVM, and report what happened as JSON on stdout.
//!
//! The repository is packed into a throwaway ext4 work drive. The guest's PID 1
//! (guest/brokkr-init) runs the command as an unprivileged user with no network
//! device, writes the result onto the work drive, and reboots, which ends the VM.
//! The runner then reads the result back off the drive. Nothing inside the guest
//! talks to the host while the command runs.

use clap::Parser;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::fs;
use std::io::Read;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

#[derive(Parser)]
struct Args {
    /// Repository to run in (already patched; copied, never modified).
    #[arg(long)]
    repo: PathBuf,
    /// Shell command to run from the repository root.
    #[arg(long)]
    cmd: String,
    /// Seconds before the command is killed inside the guest.
    #[arg(long, default_value_t = 300)]
    timeout: u64,
    /// Directory to write stdout.log, stderr.log and result.json into.
    #[arg(long)]
    out: PathBuf,
    #[arg(long, env = "BROKKR_KERNEL")]
    kernel: PathBuf,
    #[arg(long, env = "BROKKR_ROOTFS")]
    rootfs: PathBuf,
    #[arg(long, env = "BROKKR_FIRECRACKER")]
    firecracker: PathBuf,
    /// Read-only ext4 image mounted at /opt/env in the guest (interpreter and
    /// dependencies for the repository).
    #[arg(long)]
    env_drive: Option<PathBuf>,
    #[arg(long, default_value_t = 1)]
    vcpus: u32,
    #[arg(long, default_value_t = 512)]
    mem_mib: u32,
}

/// Written by brokkr-init inside the guest.
#[derive(Deserialize, Serialize)]
struct GuestResult {
    exit_code: i32,
    timed_out: bool,
    run_ms: u64,
}

#[derive(Serialize)]
struct Report {
    /// None when the sandbox itself failed; the command's result otherwise.
    guest: Option<GuestResult>,
    /// Set when the sandbox failed (not when the command failed).
    error: Option<String>,
    vm_wall_ms: u64,
    firecracker: String,
    kernel_sha256: String,
    rootfs_sha256: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    env_sha256: Option<String>,
    vcpus: u32,
    mem_mib: u32,
    network: &'static str,
}

fn main() {
    let args = Args::parse();
    let report = run(&args);
    println!("{}", serde_json::to_string_pretty(&report).unwrap());
    if report.error.is_some() {
        std::process::exit(2);
    }
}

fn run(a: &Args) -> Report {
    let mut report = Report {
        guest: None,
        error: None,
        vm_wall_ms: 0,
        firecracker: version(&a.firecracker),
        kernel_sha256: sha256_file(&a.kernel).unwrap_or_default(),
        rootfs_sha256: fs::read_to_string(a.rootfs.with_extension("ext4.sha256"))
            .map(|s| s.trim().to_string())
            .unwrap_or_else(|_| sha256_file(&a.rootfs).unwrap_or_default()),
        env_sha256: a.env_drive.as_ref().map(|p| {
            fs::read_to_string(p.with_extension("ext4.sha256"))
                .map(|s| s.trim().to_string())
                .unwrap_or_else(|_| sha256_file(p).unwrap_or_default())
        }),
        vcpus: a.vcpus,
        mem_mib: a.mem_mib,
        network: "none",
    };
    match boot(a, &mut report) {
        Ok(g) => report.guest = Some(g),
        Err(e) => report.error = Some(e),
    }
    report
}

fn boot(a: &Args, report: &mut Report) -> Result<GuestResult, String> {
    fs::create_dir_all(&a.out).map_err(|e| format!("create out: {e}"))?;
    let tmp = a.out.join(".vm");
    let _ = fs::remove_dir_all(&tmp);
    let stage = tmp.join("stage");
    fs::create_dir_all(&stage).map_err(|e| format!("create stage: {e}"))?;

    sh(Command::new("cp").arg("-a").arg(&a.repo).arg(stage.join("repo")))?;
    fs::write(stage.join("cmd"), &a.cmd).map_err(|e| e.to_string())?;
    fs::write(stage.join("timeout"), a.timeout.to_string()).map_err(|e| e.to_string())?;

    // Room for the repo plus whatever the tests write.
    let size_mib = dir_size(&stage) / (1 << 20) * 2 + 256;
    let work = tmp.join("work.ext4");
    sh(Command::new("mkfs.ext4")
        .args(["-q", "-F", "-d"])
        .arg(&stage)
        .arg(&work)
        .arg(format!("{size_mib}M")))?;

    // Drive order fixes the guest device names: rootfs vda, work vdb, env vdc.
    let mut drives = vec![
        serde_json::json!({ "drive_id": "rootfs", "path_on_host": a.rootfs, "is_root_device": true, "is_read_only": true }),
        serde_json::json!({ "drive_id": "work", "path_on_host": work, "is_root_device": false, "is_read_only": false }),
    ];
    if let Some(env) = &a.env_drive {
        let env = env.canonicalize().map_err(|e| format!("env drive {}: {e}", env.display()))?;
        drives.push(serde_json::json!({ "drive_id": "env", "path_on_host": env, "is_root_device": false, "is_read_only": true }));
    }
    let config = serde_json::json!({
        "boot-source": {
            "kernel_image_path": a.kernel,
            "boot_args": "console=ttyS0 reboot=k panic=1 pci=off quiet init=/usr/sbin/brokkr-init"
        },
        "drives": drives,
        "machine-config": { "vcpu_count": a.vcpus, "mem_size_mib": a.mem_mib }
    });
    let cfg_path = tmp.join("vm.json");
    fs::write(&cfg_path, config.to_string()).map_err(|e| e.to_string())?;

    let console = fs::File::create(a.out.join("console.log")).map_err(|e| e.to_string())?;
    let start = Instant::now();
    let mut child = Command::new(&a.firecracker)
        .arg("--no-api")
        .arg("--config-file")
        .arg(&cfg_path)
        .stdin(Stdio::null())
        .stdout(console.try_clone().map_err(|e| e.to_string())?)
        .stderr(console)
        .spawn()
        .map_err(|e| format!("spawn firecracker: {e}"))?;

    // The guest enforces the command timeout; this is the backstop for a guest
    // that never reboots.
    let deadline = Duration::from_secs(a.timeout + 60);
    loop {
        match child.try_wait().map_err(|e| e.to_string())? {
            Some(_) => break,
            None if start.elapsed() > deadline => {
                let _ = child.kill();
                let _ = child.wait();
                report.vm_wall_ms = start.elapsed().as_millis() as u64;
                return Err("microVM did not exit before the host deadline".into());
            }
            None => std::thread::sleep(Duration::from_millis(20)),
        }
    }
    report.vm_wall_ms = start.elapsed().as_millis() as u64;

    // Pull /out off the work drive. Relative paths keep debugfs away from spaces.
    sh(Command::new("debugfs")
        .arg("-R")
        .arg("rdump /out .")
        .arg(work.canonicalize().map_err(|e| e.to_string())?)
        .current_dir(&tmp))?;
    for f in ["stdout.log", "stderr.log", "result.json"] {
        let _ = fs::rename(tmp.join("out").join(f), a.out.join(f));
    }
    let _ = fs::remove_dir_all(&tmp);

    let raw = fs::read_to_string(a.out.join("result.json"))
        .map_err(|_| "guest wrote no result.json (see console.log)".to_string())?;
    serde_json::from_str(&raw).map_err(|e| format!("bad result.json: {e}"))
}

fn sh(cmd: &mut Command) -> Result<(), String> {
    let out = cmd.output().map_err(|e| format!("{cmd:?}: {e}"))?;
    if !out.status.success() {
        return Err(format!("{cmd:?}: {}", String::from_utf8_lossy(&out.stderr).trim()));
    }
    Ok(())
}

fn version(bin: &Path) -> String {
    Command::new(bin)
        .arg("--version")
        .output()
        .ok()
        .and_then(|o| String::from_utf8(o.stdout).ok())
        .and_then(|s| s.lines().next().map(str::to_string))
        .unwrap_or_default()
}

fn sha256_file(p: &Path) -> Option<String> {
    let mut f = fs::File::open(p).ok()?;
    let mut h = Sha256::new();
    let mut buf = vec![0u8; 1 << 20];
    loop {
        let n = f.read(&mut buf).ok()?;
        if n == 0 {
            break;
        }
        h.update(&buf[..n]);
    }
    Some(format!("{:x}", h.finalize()))
}

fn dir_size(p: &Path) -> u64 {
    let Ok(md) = fs::symlink_metadata(p) else { return 0 };
    if !md.is_dir() {
        return md.len();
    }
    fs::read_dir(p)
        .map(|rd| rd.flatten().map(|e| dir_size(&e.path())).sum())
        .unwrap_or(0)
}
