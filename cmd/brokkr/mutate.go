package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/4ktLuffy/brokkr/internal/mutate"
	"github.com/4ktLuffy/brokkr/internal/verify"
)

// mutateCmd scores the regression test in a patch by breaking its fix: see
// package mutate. One microVM run per mutant plus one baseline.
func mutateCmd(argv []string) {
	fs := flag.NewFlagSet("mutate", flag.ExitOnError)
	taskPath := fs.String("task", "", "live task spec (JSON)")
	repo := fs.String("repo", "", "original repository (never modified)")
	patchPath := fs.String("patch", "", "the agent's patch: a fix and a new test_brokkr_*.py")
	out := fs.String("out", "", "directory for mutate.json, mutate.md and the run logs")
	max := fs.Int("max-mutants", 20, "most mutants to run, a diverse sample; 0 runs all")
	testCmd := fs.String("test-cmd", "", "test command without test paths (default: the task's, with its trailing paths removed)")
	runner := fs.String("runner", envOr("BROKKR_RUNNER", "brokkr-runner"), "path to brokkr-runner")
	host := fs.String("host", envOr("BROKKR_HOST", hostLabel()), "machine label recorded in the evidence")
	_ = fs.Parse(argv)
	if *taskPath == "" || *repo == "" || *patchPath == "" || *out == "" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	raw, err := os.ReadFile(*taskPath)
	if err != nil {
		die(err)
	}
	var task verify.Task
	if err := json.Unmarshal(raw, &task); err != nil {
		die(fmt.Errorf("task %s: %w", *taskPath, err))
	}
	for _, p := range []*string{&task.TestPatch, &task.EnvImage} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(filepath.Dir(*taskPath), *p)
		}
	}
	if task.NewTestsDir == "" {
		die(fmt.Errorf("mutate needs a task with new_tests_dir"))
	}
	patch, err := os.ReadFile(*patchPath)
	if err != nil {
		die(err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		die(err)
	}
	if *testCmd == "" {
		*testCmd = mutate.BaseCmd(task.TestCmd)
	}
	task.TestCmd = *testCmd
	vc := verify.Config{Runner: *runner, Host: *host, Slots: envInt("BROKKR_SANDBOX_SLOTS", 0),
		SlotDir: envOr("BROKKR_SLOT_DIR", filepath.Join(os.TempDir(), "brokkr-slots"))}

	_, _, files := verify.SplitNewTests(string(patch), task.NewTestsDir, *repo)
	r := &mutate.SandboxRunner{Cfg: vc, Task: task, RepoDir: *repo, OutDir: *out, NewFiles: files}
	fmt.Fprintf(os.Stderr, "mutate: test command: %s %v\n", task.TestCmd, files)
	res, err := mutate.Test(r, task.NewTestsDir, *repo, string(patch), mutate.Options{
		MaxMutants: *max,
		Protect:    task.Protect,
		OnMutant: func(done, total int, m *mutate.Mutant) {
			fmt.Fprintf(os.Stderr, "mutate: %d/%d %-8s %s:%d %s\n", done, total, m.Status, m.Path, m.Line, m.Desc)
		},
	})
	if res != nil {
		res.TestCmd = task.TestCmd
		if werr := mutate.Write(*out, res); werr != nil {
			die(werr)
		}
	}
	if err != nil {
		die(err)
	}
	fmt.Printf("MUTATE  %s\n  %s\n", res.Line(), filepath.Join(*out, "mutate.md"))
	if res.Score == nil {
		os.Exit(2)
	}
}
