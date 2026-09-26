package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExecResult is what a sandboxed command produced.
type ExecResult struct {
	ExitCode int
	TimedOut bool
	Stdout   string
	Stderr   string
}

// Exec runs cmd in a fresh microVM against repoDir with patchPath applied (if
// any) and the task's environment image, without the hidden test patch. It is
// for the agent's own exploration; it never produces a verdict. An error means
// the sandbox failed, not the command.
func Exec(cfg Config, task Task, repoDir, patchPath, cmd string, timeoutS int, outDir string) (*ExecResult, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	stage := filepath.Join(outDir, "repo")
	_ = os.RemoveAll(stage)
	if out, err := exec.Command("cp", "-R", repoDir, stage).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("copy repo: %v: %s", err, out)
	}
	defer os.RemoveAll(stage)
	if patchPath != "" {
		patch, err := os.ReadFile(patchPath)
		if err != nil {
			return nil, err
		}
		apply := exec.Command("git", "apply", "--whitespace=nowarn", "-")
		apply.Dir = stage
		apply.Stdin = bytes.NewReader(patch)
		if out, err := apply.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("patch does not apply: %s", strings.TrimSpace(string(out)))
		}
	}
	args := []string{"--repo", stage, "--cmd", cmd, "--timeout", fmt.Sprint(timeoutS), "--out", outDir}
	if task.EnvImage != "" {
		args = append(args, "--env-drive", task.EnvImage)
	}
	if task.MemMiB > 0 {
		args = append(args, "--mem-mib", fmt.Sprint(task.MemMiB))
	}
	report, _ := exec.Command(cfg.Runner, args...).Output()
	var rep struct {
		Guest *struct {
			ExitCode int  `json:"exit_code"`
			TimedOut bool `json:"timed_out"`
		} `json:"guest"`
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(report, &rep); err != nil {
		return nil, fmt.Errorf("runner output unreadable: %v", err)
	}
	if rep.Error != nil || rep.Guest == nil {
		msg := "no guest result"
		if rep.Error != nil {
			msg = *rep.Error
		}
		return nil, fmt.Errorf("sandbox failed: %s", msg)
	}
	stdout, _ := os.ReadFile(filepath.Join(outDir, "stdout.log"))
	stderr, _ := os.ReadFile(filepath.Join(outDir, "stderr.log"))
	return &ExecResult{ExitCode: rep.Guest.ExitCode, TimedOut: rep.Guest.TimedOut, Stdout: string(stdout), Stderr: string(stderr)}, nil
}
