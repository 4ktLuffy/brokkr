// Package bundle turns a finished `brokkr fix` run into what a reviewer needs to
// decide on it: the patch and a pull-request description carrying the
// verification evidence. It never pushes, opens or merges anything; a human
// does that, which is the approval gate.
package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type summary struct {
	Task         string         `json:"task"`
	Model        string         `json:"model"`
	Harness      string         `json:"harness"`
	Verdict      string         `json:"verdict"`
	Reasons      []string       `json:"reasons"`
	AgentClaimed bool           `json:"agent_claimed_fixed"`
	StopReason   string         `json:"stop_reason"`
	Turns        int            `json:"turns"`
	TestRuns     int            `json:"test_runs"`
	PythonRuns   int            `json:"python_runs"`
	PromptTok    int            `json:"prompt_tokens"`
	OutputTok    int            `json:"completion_tokens"`
	WallMS       int64          `json:"wall_ms"`
	Host         string         `json:"host"`
	Sampling     map[string]any `json:"sampling"`
	SelfTest     string         `json:"self_test"`
}

// selfTest is verify.SelfResult as written to selftest/selftest.json.
type selfTest struct {
	Verdict     string   `json:"verdict"`
	Reasons     []string `json:"reasons"`
	NewFiles    []string `json:"new_test_files"`
	NewTests    []string `json:"new_tests"`
	FailBefore  []string `json:"new_tests_failing_before"`
	Regressions []string `json:"regressions"`
	RunA        string   `json:"run_a"`
	RunB        string   `json:"run_b"`
}

type evidence struct {
	Verifier string `json:"verifier"`
	RunID    string `json:"run_id"`
	Task     struct {
		Name          string   `json:"name"`
		Issue         string   `json:"issue"`
		RequiredTests []string `json:"required_tests"`
	} `json:"task"`
	Inputs struct {
		TestPatchSHA256   string `json:"test_patch_sha256"`
		RepoTreeSHA256    string `json:"repo_tree_sha256"`
		PatchSHA256       string `json:"patch_sha256"`
		PatchedTreeSHA256 string `json:"patched_tree_sha256"`
	} `json:"inputs"`
	Verdict string   `json:"verdict"`
	Reasons []string `json:"reasons"`
	Tests   struct {
		Passed          []string `json:"passed"`
		MissingRequired []string `json:"missing_required"`
	} `json:"tests"`
	Sandbox struct {
		Firecracker  string `json:"firecracker"`
		KernelSHA256 string `json:"kernel_sha256"`
		RootfsSHA256 string `json:"rootfs_sha256"`
		EnvSHA256    string `json:"env_sha256"`
		Network      string `json:"network"`
		VMWallMS     int64  `json:"vm_wall_ms"`
	} `json:"sandbox"`
}

// Result says what was written and whether the run is fit to propose.
type Result struct {
	Ready     bool   // verified PASS
	PatchPath string // patch.diff
	BodyPath  string // pr.md
	Title     string
}

// Write reads runDir (a `brokkr fix` output directory) and writes patch.diff
// and pr.md into outDir. A run that did not verify still gets a bundle, marked
// NOT READY, because a reviewer may want to see why.
func Write(runDir, outDir string) (*Result, error) {
	var sum summary
	if err := readJSON(filepath.Join(runDir, "summary.json"), &sum); err != nil {
		return nil, fmt.Errorf("summary.json: %w", err)
	}
	patch, err := os.ReadFile(filepath.Join(runDir, "final.patch"))
	if err != nil {
		return nil, fmt.Errorf("final.patch: %w", err)
	}
	var ev evidence
	haveEv := readJSON(filepath.Join(runDir, "final", "evidence.json"), &ev) == nil
	// Live mode: the agent's own regression test, checked by Brokkr.
	var st selfTest
	live := readJSON(filepath.Join(runDir, "selftest", "selftest.json"), &st) == nil
	hidden := haveEv
	if live && !haveEv {
		// No hidden tests: run B of the self-test (the whole patch) carries
		// the issue text and the sandbox and input hashes.
		haveEv = readJSON(filepath.Join(runDir, "selftest", "B", "evidence.json"), &ev) == nil
	}
	agentSummary := lastSubmitSummary(filepath.Join(runDir, "transcript.jsonl"))

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	res := &Result{
		Ready:     sum.Verdict == "PASS" && haveEv && (ev.Verdict == "PASS" || live && !hidden && st.Verdict == "PASS"),
		PatchPath: filepath.Join(outDir, "patch.diff"),
		BodyPath:  filepath.Join(outDir, "pr.md"),
		Title:     title(sum.Task, ev.Task.Issue),
	}
	if err := os.WriteFile(res.PatchPath, patch, 0o644); err != nil {
		return nil, err
	}

	var b strings.Builder
	status := "READY FOR REVIEW: verified in a sandbox against the task's tests"
	if live && !hidden {
		status = "READY FOR REVIEW: the agent's regression test fails without the fix and passes with it, in a sandbox, and no existing test broke"
	}
	if !res.Ready {
		status = "NOT READY: the patch did not verify (" + strings.Join(sum.Reasons, "; ") + ")"
	}
	fmt.Fprintf(&b, "## %s\n\n**%s**\n\n", res.Title, status)
	if agentSummary != "" {
		fmt.Fprintf(&b, "Agent's description of the change: %s\n\n", agentSummary)
	}
	claim := "said it fixed the issue"
	if !sum.AgentClaimed {
		claim = "did not claim a fix"
	}
	match := "matches"
	if sum.AgentClaimed != (sum.Verdict == "PASS") {
		match = "**does not match**"
	}
	fmt.Fprintf(&b, "The agent %s; the verdict below comes from Brokkr re-running the tests, and it %s the claim.\n\n", claim, match)

	fmt.Fprintf(&b, "### Verification\n\n| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| Verdict | %s |\n", sum.Verdict)
	fmt.Fprintf(&b, "| Reasons | %s |\n", strings.Join(sum.Reasons, "; "))
	if live {
		fmt.Fprintf(&b, "| Regression test (agent-written) | %s: %s |\n", st.Verdict, strings.Join(st.Reasons, "; "))
		fmt.Fprintf(&b, "| New tests failing before the fix | %s |\n", orNone(strings.Join(st.FailBefore, ", "), "none"))
		fmt.Fprintf(&b, "| Existing tests broken | %d |\n", len(st.Regressions))
		fmt.Fprintf(&b, "| Self-test runs (original + test / whole patch) | %s / %s |\n", st.RunA, st.RunB)
	}
	if hidden {
		req := len(ev.Task.RequiredTests)
		fmt.Fprintf(&b, "| Required tests passing | %d of %d |\n", req-len(ev.Tests.MissingRequired), req)
		fmt.Fprintf(&b, "| Hidden test patch | %s |\n", orNone(short(ev.Inputs.TestPatchSHA256), "none"))
	}
	if haveEv {
		fmt.Fprintf(&b, "| Repo tree / patch / patched tree | %s / %s / %s |\n",
			short(ev.Inputs.RepoTreeSHA256), short(ev.Inputs.PatchSHA256), short(ev.Inputs.PatchedTreeSHA256))
		network := ev.Sandbox.Network
		if network == "none" {
			network = "no network device (loopback only)"
		}
		fmt.Fprintf(&b, "| Sandbox | %s, %s, kernel %s, rootfs %s, env %s |\n",
			ev.Sandbox.Firecracker, network, short(ev.Sandbox.KernelSHA256), short(ev.Sandbox.RootfsSHA256), orNone(short(ev.Sandbox.EnvSHA256), "none"))
		fmt.Fprintf(&b, "| Verifier / run | %s / %s |\n", orNone(ev.Verifier, "0.1"), ev.RunID)
	}
	fmt.Fprintf(&b, "\n### How the agent worked\n\n| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| Model | %s (harness %s) |\n", sum.Model, sum.Harness)
	fmt.Fprintf(&b, "| Turns / test runs / reproductions | %d / %d / %d |\n", sum.Turns, sum.TestRuns, sum.PythonRuns)
	fmt.Fprintf(&b, "| Tokens in / out | %d / %d |\n", sum.PromptTok, sum.OutputTok)
	fmt.Fprintf(&b, "| Wall time | %.1f min |\n", float64(sum.WallMS)/60000)
	fmt.Fprintf(&b, "| Stopped because | %s |\n", sum.StopReason)
	fmt.Fprintf(&b, "| Machine | %s |\n", sum.Host)
	fmt.Fprintf(&b, "\nFull evidence: `summary.json`, `transcript.jsonl` and `final/evidence.json` in the run directory.\n")
	fmt.Fprintf(&b, "\nNothing was pushed. A human reviews this and opens the pull request.\n")
	if err := os.WriteFile(res.BodyPath, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	return res, nil
}

func readJSON(p string, v any) error {
	raw, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// lastSubmitSummary returns the summary argument of the agent's final submit.
func lastSubmitSummary(transcript string) string {
	raw, err := os.ReadFile(transcript)
	if err != nil {
		return ""
	}
	out := ""
	for _, line := range strings.Split(string(raw), "\n") {
		var m struct {
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		}
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		for _, c := range m.ToolCalls {
			if c.Function.Name == "submit" {
				var a struct {
					Summary string `json:"summary"`
				}
				if json.Unmarshal([]byte(c.Function.Arguments), &a) == nil && a.Summary != "" {
					out = a.Summary
				}
			}
		}
	}
	return out
}

func title(task, issue string) string {
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(issue), "\n", 2)[0])
	if first == "" {
		return "Fix " + task
	}
	if len(first) > 90 {
		first = first[:87] + "..."
	}
	return first
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func orNone(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
