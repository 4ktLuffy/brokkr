package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/4ktLuffy/brokkr/internal/minimize"
	"github.com/4ktLuffy/brokkr/internal/verify"
)

// minimizeCmd shrinks a patch that verifies to the smallest one that still
// does. Every candidate is judged by the real verifier (or, for a live task,
// the self-test), in a fresh microVM.
func minimizeCmd(argv []string) {
	fs := flag.NewFlagSet("minimize", flag.ExitOnError)
	taskPath := fs.String("task", "", "task spec (JSON)")
	repo := fs.String("repo", "", "repository (never modified)")
	patchPath := fs.String("patch", "", "the patch to minimize (it must PASS)")
	out := fs.String("out", "", "directory for minimal.patch, minimize.json and the per-run evidence")
	maxRuns := fs.Int("max-runs", 40, "verifier runs allowed (each boots microVMs); 0 is no limit")
	gran := fs.String("granularity", "hunk", "hunk, or line (then also single changed lines)")
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
	if task.Live && task.NewTestsDir == "" {
		die(fmt.Errorf("live task needs new_tests_dir"))
	}
	vc := verify.Config{Runner: *runner, Host: *host, Slots: envInt("BROKKR_SANDBOX_SLOTS", 0),
		SlotDir: envOr("BROKKR_SLOT_DIR", filepath.Join(os.TempDir(), "brokkr-slots"))}
	patch, err := os.ReadFile(*patchPath)
	if err != nil {
		die(err)
	}

	n := 0
	oracle := func(text string) (string, string, error) {
		n++
		dir := filepath.Join(*out, "runs", fmt.Sprintf("%03d", n))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", "", err
		}
		cand := ""
		if text != "" {
			cand = filepath.Join(dir, "candidate.patch")
			if err := os.WriteFile(cand, []byte(text), 0o644); err != nil {
				return "", "", err
			}
		}
		var v verify.Verdict
		var id string
		if task.Live {
			// A candidate without the regression test cannot pass; skip the boot.
			if _, _, files := verify.SplitNewTests(text, task.NewTestsDir, *repo); len(files) == 0 {
				return string(verify.Fail), "", nil
			}
			st, err := verify.SelfTest(vc, task, *repo, cand, dir)
			if err != nil {
				return "", "", err
			}
			v, id = st.Verdict, st.RunB
		} else {
			ev, err := verify.Run(vc, task, *repo, cand, dir)
			if err != nil {
				return "", "", err
			}
			v, id = ev.Verdict, ev.RunID
		}
		// An ERROR says nothing about the candidate; guessing FAIL would remove
		// a needed hunk, so stop instead.
		if v == verify.Error {
			return "", id, fmt.Errorf("run %03d: sandbox error, not a verdict about the patch", n)
		}
		return string(v), id, nil
	}

	rep, err := minimize.Minimize(string(patch), minimize.Config{
		Oracle: oracle, Applies: minimize.GitApplies(*repo), Granularity: *gran, MaxRuns: *maxRuns,
	})
	if rep != nil {
		if werr := rep.Write(*out); werr != nil {
			die(werr)
		}
		fmt.Print(rep.Summary())
	}
	if err != nil {
		die(err)
	}
	fmt.Printf("wrote %s\n", filepath.Join(*out, "minimal.patch"))
	if !rep.OneMinimal {
		os.Exit(1)
	}
}
