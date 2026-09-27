// Package agent runs a model in a tool-calling loop to fix a task.
//
// The model never executes anything on the host. It can read files, make exact
// text replacements in a working copy, and ask for the tests to run, which
// happens in a fresh microVM through the verifier. When it stops, the final diff
// is verified once more from scratch, and that verdict, not the model's own
// claim, is the result.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/4ktLuffy/brokkr/internal/model"
	"github.com/4ktLuffy/brokkr/internal/route"
	"github.com/4ktLuffy/brokkr/internal/verify"
)

// HarnessVersion identifies the agent loop, tools and prompts. Bump it whenever
// any of them change, so results from different harnesses are never pooled.
//
//	0.1.0  first eval (strict edits; reindent added mid-way, see edit_mode)
//	0.2.0  no-change run_tests is free; budget refusal no longer says "submit now";
//	       claims made against the agent's own failing test run are recorded
//	0.3.0  tools for real repositories: list_dir, search, ranged read_file,
//	       run_python (sandboxed); older tool outputs shortened; hidden-test
//	       tasks show only the issue, and run_tests runs visible tests only;
//	       a malformed model reply is retried twice and then scored as the
//	       model's failure, not excused as an infra error; older tool outputs
//	       are shortened in batches above compact_above tokens (cache-friendly)
//	0.3.1  claim_against_own_tests counts VISIBLE_PASS as the agent's own pass
//	       (a recorded field only; agent behaviour identical to 0.3.0)
//	0.4.0  prompt: reproduce before editing and confirm after, and existing tests
//	       are not evidence of a fix; on hidden-test tasks a fixed=true submit
//	       with no run_python since the last edit gets one reminder
//	0.5.0  replace_lines tool; a failing call repeated 3 times gets a blunt
//	       instruction, and 6 times ends the run as stuck (scored)
//	0.6.0  any identical call repeated with no edit in between: a warning at 3,
//	       stuck (scored) at 5; 0.5's failing-call rule kept
//	0.6.1  the shrinking-prompt truncation rule applies only when the estimate is
//	       within 80% of the window (0.6.0 stopped a Codestral run at 13K of
//	       256K on a 4% drop in Mistral's count). Not run on held-out tonight.
//	0.7.0  after an edit to a .py file, the file is parsed (ast.parse, never
//	       executed) and a SyntaxError is reported in the tool result
//	0.8.0  commit nudges when nothing has been edited (at a third and two
//	       thirds of the turns, and after 3 reproductions); a turn/sandbox
//	       budget footer on every tool result; prompt: commit early, and a
//	       fix may need the same change in several places (results/ANALYSIS.md)
//	0.9.0  live mode (task.live): a create_file tool for a new regression test,
//	       and the run is judged by that test (verify.SelfTest). Tasks that are
//	       not live see the same tools and prompts as 0.8.0.
const HarnessVersion = "0.9.0"

// Chatter is what the agent needs from a model: *model.Client, or a
// *route.Router over several.
type Chatter interface {
	Chat(ctx context.Context, msgs []model.Message, tools []model.Tool) (model.Message, model.Usage, error)
}

type Config struct {
	// Model is the model (with Router set: the primary backend's client, used
	// for the summary's model and sampling fields).
	Model *model.Client
	// Router, when set, serves every model call; see package route.
	Router      *route.Router
	Verify      verify.Config
	MaxTurns    int // model calls
	MaxTestRuns int // run_tests calls, each a microVM boot
	// ContextTokens is the model's context window as served. Servers such as
	// Ollama truncate an oversized prompt silently and report the truncated
	// size (scripts/probe-context.py), so the reported count cannot be used to
	// detect it. Instead the run stops as an infrastructure error when the
	// conversation's estimated size exceeds this window, or when the reported
	// prompt size shrinks between turns, which cannot happen without truncation
	// because the conversation only grows.
	ContextTokens int
	// StrictEdits restores the original replace_in_file: exact match only, no
	// indentation adjustment, no hint and no echo of the edited region. For
	// ablations.
	StrictEdits bool
	// CompactAbove is the estimated prompt size, in tokens, above which older
	// tool outputs are shortened (see compactor). 0 never shortens.
	CompactAbove int
}

type Summary struct {
	Schema  string         `json:"schema"`
	Task    string         `json:"task"`
	Model   string         `json:"model"`
	Host    string         `json:"host"`
	Verdict verify.Verdict `json:"verdict"`
	Reasons []string       `json:"reasons"`
	// Infra is set when the run was cut short by the model server, not the
	// agent. Such runs are reported separately and never scored as agent failures.
	Infra        string         `json:"infra_error,omitempty"`
	EditMode     string         `json:"edit_mode"`
	Sampling     map[string]any `json:"sampling"`
	MaxPrompt    int            `json:"max_prompt_tokens"`
	AgentClaimed bool           `json:"agent_claimed_fixed"`
	// LastOwnTest is the verdict of the agent's own last run_tests, if any.
	// ClaimAgainstOwnTests marks a "fixed" claim made when that run had not
	// passed: the agent had evidence it was wrong and claimed success anyway.
	LastOwnTest          string         `json:"last_own_test,omitempty"`
	ClaimAgainstOwnTests bool           `json:"claim_against_own_tests"`
	Harness              string         `json:"harness"`
	ParkedUntil          string         `json:"parked_until,omitempty"` // free tier exhausted; see model.ErrBudgetParked
	StopReason           string         `json:"stop_reason"`
	Turns                int            `json:"turns"`
	TestRuns             int            `json:"test_runs"`
	PythonRuns           int            `json:"python_runs"`
	ToolCalls            map[string]int `json:"tool_calls"`
	Refusals             int            `json:"refusals"`
	// MalformedReplies counts replies the server could not parse (for example
	// a broken tool call). Retried; a run that ends on one is scored.
	MalformedReplies       int            `json:"malformed_replies"`
	CompactBatches         int            `json:"compact_batches"`
	SubmitReminders        int            `json:"submit_reminders"`
	LoopWarnings           int            `json:"loop_warnings"`
	SyntaxErrorsIntroduced int            `json:"syntax_errors_introduced"`
	CommitNudges           int            `json:"commit_nudges"`
	Budget                 map[string]int `json:"budget"`
	PromptTok              int            `json:"prompt_tokens"`
	OutputTok              int            `json:"completion_tokens"`
	WallMS                 int64          `json:"wall_ms"`
	// Routed runs only: turns per backend, and each fallback. More than one
	// backend makes a mixed run, left out of model comparisons.
	ServedBy      map[string]int `json:"served_by,omitempty"`
	RouteSwitches []route.Switch `json:"route_switches,omitempty"`
	// Live mode only: the verdict of the agent's own regression test.
	SelfTest        verify.Verdict `json:"self_test,omitempty"`
	SelfTestReasons []string       `json:"self_test_reasons,omitempty"`
	PatchLines      int            `json:"patch_changed_lines"`
}

const systemPrompt = `You are fixing an issue in a code repository. You change source files; you cannot change tests.

How to work:
1. Find the relevant code: search for names from the issue, list_dir to look around, read_file to read (in line ranges for big files).
2. Reproduce the issue with run_python before you edit: turn the issue's example (or a minimal version of the problem) into a small program that prints the wrong behaviour.
3. Understand the cause, then make the smallest change that fixes it. Edit with replace_in_file: old_text must match the file exactly, once, without line numbers.
4. Run your reproduction again and confirm it now prints the correct behaviour. Then run run_tests to check you broke nothing.
5. Call submit. Set fixed=true only if your reproduction now shows the correct behaviour; otherwise fixed=false.

Passing the existing tests is not evidence of a fix: those tests did not catch this issue in the first place. Hidden tests written for this issue will judge your change.

Commit early: once you have found the likely cause, make an edit, then check it and refine. Reading and reproducing without editing does not fix anything, and your turns are limited (each tool result shows how many are left). A fix may need the same change in more than one place: search for other uses of what you change.

Rules:
- Paths marked protected cannot be edited. Fix the code, not the tests.
- Sandbox runs (run_tests, run_python) are limited; use them for real checks.
- Keep going until the issue is fixed or you are sure you cannot fix it.`

// livePrompt replaces the hidden-tests paragraph for live tasks, where the
// agent's own regression test is the evidence.
const livePrompt = `No tests have been written for this issue. You write one: use create_file to add a new test file %stest_brokkr_<short_name>.py containing pytest tests that fail on the current code because of this issue, and pass once it is fixed. Brokkr will run your test on the original code (it must fail) and on your change (it must pass), and check that no existing test breaks. A test that passes without your fix proves nothing. You cannot change existing tests.`

func Run(ctx context.Context, cfg Config, task verify.Task, repoDir, outDir string) (*Summary, error) {
	start := time.Now()
	sum := &Summary{
		Schema: "brokkr.agent-run/v0", Harness: HarnessVersion, Task: task.Name, Model: cfg.Model.Model,
		Host: cfg.Verify.Host, ToolCalls: map[string]int{}, EditMode: "reindent",
	}
	if cfg.StrictEdits {
		sum.EditMode = "strict"
	}
	sum.Sampling = map[string]any{
		"temperature": cfg.Model.Temperature, "top_p": cfg.Model.TopP,
		"presence_penalty": cfg.Model.PresencePenalty, "reasoning_effort": cfg.Model.ReasoningEffort,
		"max_reply_tokens": cfg.Model.MaxTokens, "context_tokens": cfg.ContextTokens,
	}
	sum.Budget = map[string]int{"max_turns": cfg.MaxTurns, "max_sandbox_runs": cfg.MaxTestRuns, "compact_above": cfg.CompactAbove}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	orig := filepath.Join(outDir, "a")
	work := filepath.Join(outDir, "b")
	for _, d := range []string{orig, work} {
		_ = os.RemoveAll(d)
		if out, err := exec.Command("cp", "-R", repoDir, d).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("copy repo: %v: %s", err, out)
		}
	}
	defer os.RemoveAll(orig)
	defer os.RemoveAll(work)

	tr, err := os.Create(filepath.Join(outDir, "transcript.jsonl"))
	if err != nil {
		return nil, err
	}
	defer tr.Close()
	logMsg := func(m model.Message) { b, _ := json.Marshal(m); tr.Write(append(b, '\n')) }

	ws := &workspace{root: work, orig: orig, protect: task.Protect, strict: cfg.StrictEdits}
	if task.Live {
		ws.newTests = task.NewTestsDir
		if ws.newTests == "" {
			return nil, errors.New("live task has no new_tests_dir")
		}
	}
	tools := toolDefs(task.Live)

	// Reproduce first, so the model starts from the real failure.
	// A live task without hidden tests has nothing that must fail yet: its
	// baseline only records what the existing tests do.
	bc := cfg.Verify
	bc.CollectOnly = task.Live && task.TestPatch == ""
	baseline, err := verify.Run(bc, task, repoDir, "", filepath.Join(outDir, "baseline"))
	if err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}
	if baseline.Verdict != verify.Fail && !(bc.CollectOnly && baseline.Verdict == verify.Collected) {
		return nil, fmt.Errorf("baseline verdict is %s, expected FAIL: nothing to fix, or the sandbox is broken", baseline.Verdict)
	}
	if bc.CollectOnly && len(baseline.Tests.Passed) == 0 {
		return nil, errors.New("live baseline: no existing test passed, so regressions could not be detected (check test_cmd)")
	}

	issue := task.Issue
	if issue == "" {
		issue = "The test suite fails. Make it pass."
	}
	var first string
	if task.Live {
		// Live: the issue, and the agent's own test as the evidence. Hidden
		// tests, if the task has them (historical evaluation), stay hidden.
		first = fmt.Sprintf("Issue %s:\n\n%s\n\n"+livePrompt+" run_tests runs the repository's existing tests for the affected area plus your new test files. Protected (read-only): %s",
			task.Name, issue, task.NewTestsDir, strings.Join(task.Protect, ", "))
	} else if task.TestPatch != "" {
		// Hidden tests: as in SWE-bench, the agent gets the issue and nothing
		// from the tests that will judge it. The baseline above ran them only
		// to confirm the task fails as it should.
		first = fmt.Sprintf("Issue %s:\n\n%s\n\nYour fix will be judged by tests you cannot see. run_tests runs the repository's existing tests for the affected area. Protected (read-only): %s",
			task.Name, issue, strings.Join(task.Protect, ", "))
	} else {
		first = fmt.Sprintf("Task: %s\n\n%s\n\nProtected (read-only): %s\n\nCurrent test output:\n%s",
			task.Name, issue, strings.Join(task.Protect, ", "), testOutput(filepath.Join(outDir, "baseline"), baseline))
	}
	msgs := []model.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: first},
	}
	for _, m := range msgs {
		logMsg(m)
	}

	attempt := 0
	prevPrompt, prevEst := 0, 0
	var chat Chatter = cfg.Model
	window := func() int { return cfg.ContextTokens }
	switches := func() int { return 0 }
	if cfg.Router != nil {
		chat = cfg.Router
		// The conversation may grow to the largest backend's window: the
		// router moves it there when it outgrows the current one.
		window = cfg.Router.MaxContext
		switches = func() int { return len(cfg.Router.Switches) }
	}
	checkedSinceEdit, reminded := false, false
	lastFailSig, repeats := "", 0
	editsMade := 0                // successful edits so far
	pythonCalls := 0              // run_python attempts, including ones refused for budget
	nudged := map[string]bool{}   // commit nudges already given
	sinceEdit := map[string]int{} // identical non-sandbox calls since the last successful edit
	comp := &compactor{High: cfg.CompactAbove, Keep: 3}
loop:
	for {
		if sum.Turns >= cfg.MaxTurns {
			sum.StopReason = "turn budget exhausted"
			break
		}
		view := comp.view(msgs)
		est := estimateTokens(view)
		if w := window(); w > 0 && est > w {
			sum.StopReason = "context window full"
			sum.Infra = fmt.Sprintf("conversation is about %d tokens, over the %d-token window; the server would truncate it",
				est, w)
			break
		}
		switchesBefore := switches()
		reply, usage, err := chat.Chat(ctx, view, tools)
		// A malformed reply is resampled up to twice; the sampling differs
		// each time. Only then does it end the run, scored as a failure.
		for retry := 0; retry < 2; retry++ {
			var bad *model.ErrMalformedReply
			if !errors.As(err, &bad) {
				break
			}
			sum.MalformedReplies++
			reply, usage, err = chat.Chat(ctx, view, tools)
		}
		sum.Turns++
		sum.PromptTok += usage.PromptTokens
		sum.OutputTok += usage.CompletionTokens
		sum.MaxPrompt = max(sum.MaxPrompt, usage.PromptTokens)
		// Truncation: the server counted fewer tokens than last turn although
		// what we sent grew. Shortening old outputs shrinks both, so it cannot
		// trip this.
		// Only near the window: est over-estimates, so while it is well under
		// the served window nothing can have been cut, and small drops in the
		// reported count are the provider's own accounting (seen with Mistral
		// at 13K of 256K, harness 0.6.0).
		// A switch to another backend changes the tokenizer: its count is
		// not comparable with the last one.
		if switches() != switchesBefore {
			prevPrompt, prevEst = 0, 0
		}
		cur := window()
		if cfg.Router != nil {
			cur = cfg.Router.Current().ContextTokens
		}
		nearWindow := cur > 0 && est*5 >= cur*4
		truncated := nearWindow && usage.PromptTokens > 0 && usage.PromptTokens < prevPrompt && est >= prevEst
		shrankFrom := prevPrompt
		prevPrompt, prevEst = usage.PromptTokens, est
		if err != nil {
			var parked *model.ErrBudgetParked
			var bad *model.ErrMalformedReply
			switch {
			case errors.As(err, &bad):
				// The model's own failure: scored, not excused.
				sum.MalformedReplies++
				sum.StopReason = "malformed model reply (after retries)"
				break loop
			case errors.As(err, &parked):
				sum.StopReason = "budget parked"
				sum.ParkedUntil = parked.ResumeAt
			default:
				sum.StopReason = "model error"
			}
			sum.Infra = "model error: " + err.Error()
			break
		}
		if truncated {
			sum.StopReason = "context truncated"
			sum.Infra = fmt.Sprintf("reported prompt shrank from %d to %d tokens although the conversation grew; the server truncated it",
				shrankFrom, usage.PromptTokens)
			break
		}
		if reply.Content == "" && len(reply.ToolCalls) == 0 {
			reply.Content = "(empty)"
		}
		reply.Role = "assistant"
		msgs = append(msgs, reply)
		logMsg(reply)

		if len(reply.ToolCalls) == 0 {
			nudge := model.Message{Role: "user", Content: "Use the tools. Call submit when you are done."}
			msgs = append(msgs, nudge)
			logMsg(nudge)
			continue
		}
		for _, call := range reply.ToolCalls {
			name := call.Function.Name
			sum.ToolCalls[name]++
			var args map[string]any
			_ = json.Unmarshal([]byte(call.Function.Arguments), &args)

			var result string
			sig := name + "\x00" + call.Function.Arguments
			switch name {
			case "submit":
				if claimed, _ := args["fixed"].(bool); claimed && (task.TestPatch != "" || task.Live) && !checkedSinceEdit && !reminded {
					// One reminder, once per run: a fix claimed without re-running
					// a reproduction since the last edit is a guess.
					reminded = true
					sum.SubmitReminders++
					result = "Not submitted yet: you have not run a reproduction with run_python since your last edit. Run one that shows the issue's behaviour is now correct, then submit again. If you cannot, submit with fixed=false."
					break
				}
				sum.AgentClaimed, _ = args["fixed"].(bool)
				// On hidden-test tasks the agent's own runs are VISIBLE_PASS or
				// VISIBLE_FAIL; either kind of pass counts as evidence for a claim.
				ownPass := sum.LastOwnTest == string(verify.Pass) || sum.LastOwnTest == "VISIBLE_"+string(verify.Pass)
				sum.ClaimAgainstOwnTests = sum.AgentClaimed && !ownPass
				sum.StopReason = "agent submitted"
				break loop
			case "run_tests":
				// Nothing to run yet: answer without booting a VM and without
				// spending the budget, which exists to bound microVM boots.
				if patch, err := ws.diff(); err == nil && patch == "" && task.TestPatch == "" {
					result = "no changes yet; the tests still fail as shown at the start"
					break
				}
				if sum.TestRuns+sum.PythonRuns >= cfg.MaxTestRuns {
					result = "refused: sandbox run budget exhausted. Call submit; set fixed=true only if your checks showed the fix works."
					sum.Refusals++
					break
				}
				sum.TestRuns++
				attempt++
				var v verify.Verdict
				result, v = ws.runTests(cfg.Verify, task, filepath.Join(outDir, fmt.Sprintf("attempt-%d", attempt)))
				sum.LastOwnTest = string(v)
			case "run_python":
				pythonCalls++
				if sum.TestRuns+sum.PythonRuns >= cfg.MaxTestRuns {
					result = "refused: sandbox run budget exhausted. Call submit; set fixed=true only if your checks showed the fix works."
					sum.Refusals++
					break
				}
				code, _ := args["code"].(string)
				if strings.TrimSpace(code) == "" {
					result = "error: code is empty"
					break
				}
				sum.PythonRuns++
				attempt++
				checkedSinceEdit = true
				result = ws.runPython(cfg.Verify, task, code, filepath.Join(outDir, fmt.Sprintf("python-%d", attempt)))
			default:
				var refused bool
				result, refused = ws.call(name, args)
				if isEdit(name) && strings.HasPrefix(result, "ok:") {
					checkedSinceEdit = false
					path, _ := args["path"].(string)
					if msg := ws.syntaxError(path); msg != "" {
						result += "\n\nWARNING: after this edit the file no longer parses: " + msg + "\nFix this before anything else."
						sum.SyntaxErrorsIntroduced++
					}
				}
				if refused {
					sum.Refusals++
				}
			}
			// Loops. (a) The same call failing again and again. (b) Any identical
			// call repeated with no successful edit in between: its result
			// cannot have changed, so repeating it is not progress. Both end
			// the run as stuck, which is scored.
			if strings.HasPrefix(result, "error:") && sig == lastFailSig {
				repeats++
			} else if strings.HasPrefix(result, "error:") {
				lastFailSig, repeats = sig, 1
			} else {
				lastFailSig, repeats = "", 0
			}
			if edited := isEdit(name) && strings.HasPrefix(result, "ok:"); edited {
				sinceEdit = map[string]int{}
			} else if !strings.HasPrefix(result, "error:") && name != "run_tests" && name != "run_python" && name != "submit" {
				sinceEdit[sig]++ // failing calls are rule (a)'s business
			}
			same := sinceEdit[sig]
			switch {
			case repeats >= stuckAfter:
				sum.StopReason = fmt.Sprintf("stuck: the same failing %s call %d times", name, repeats)
			case same >= stuckIdentical:
				sum.StopReason = fmt.Sprintf("stuck: the same %s call %d times with no edit in between", name, same)
			}
			if strings.HasPrefix(sum.StopReason, "stuck:") {
				logMsg(model.Message{Role: "tool", Content: result, ToolCallID: call.ID, Name: name})
				break loop
			}
			if repeats >= 3 {
				result += fmt.Sprintf("\n\nYou have sent this exact call %d times and it failed each time. Do something different: read the lines again with read_file, then use replace_lines with the line numbers, or a shorter old_text.", repeats)
				sum.LoopWarnings++
			} else if same >= 3 {
				result += fmt.Sprintf("\n\nYou have made this exact call %d times since your last edit and its result has not changed. Stop repeating it: decide on a change and make it, or submit with fixed=false.", same)
				sum.LoopWarnings++
			}
			if isEdit(name) && strings.HasPrefix(result, "ok:") {
				editsMade++
			}
			// Commit nudges: the most common failure is never editing at
			// all (results/ANALYSIS.md). Each fires at most once.
			if editsMade == 0 && (task.TestPatch != "" || task.Live) {
				var why string
				switch {
				case sum.Turns >= 2*cfg.MaxTurns/3 && !nudged["two_thirds"]:
					nudged["two_thirds"] = true
					why = "two thirds of your turns are used"
				case sum.Turns >= cfg.MaxTurns/3 && !nudged["one_third"]:
					nudged["one_third"] = true
					why = "a third of your turns are used"
				case pythonCalls >= 3 && !nudged["reproduced"]:
					nudged["reproduced"] = true
					why = "you have reproduced the problem several times"
				}
				if why != "" {
					sum.CommitNudges++
					result += "\n\nYou have not changed any file yet, and " + why + ". You know enough to try: make your best edit now (replace_in_file or replace_lines), then check it with run_python and refine. An imperfect edit you can test is better than none."
				}
			}
			result += fmt.Sprintf("\n[turn %d of %d; sandbox runs %d of %d]", sum.Turns, cfg.MaxTurns, sum.TestRuns+sum.PythonRuns, cfg.MaxTestRuns)
			tm := model.Message{Role: "tool", Content: result, ToolCallID: call.ID, Name: name}
			msgs = append(msgs, tm)
			logMsg(tm)
		}
	}

	sum.CompactBatches = comp.Batches
	if cfg.Router != nil {
		sum.ServedBy, sum.RouteSwitches = cfg.Router.ServedBy, cfg.Router.Switches
	}

	// The verdict comes from a fresh verification of the final diff.
	patch, err := ws.diff()
	if err != nil {
		return nil, err
	}
	patchPath := filepath.Join(outDir, "final.patch")
	if err := os.WriteFile(patchPath, []byte(patch), 0o644); err != nil {
		return nil, err
	}
	for _, l := range strings.Split(patch, "\n") {
		if (strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-")) &&
			!strings.HasPrefix(l, "+++") && !strings.HasPrefix(l, "---") {
			sum.PatchLines++
		}
	}
	switch {
	case patch == "":
		sum.Verdict, sum.Reasons = verify.Fail, []string{"agent produced no change"}
	case task.Live:
		// The agent's own test is the evidence (verify.SelfTest). When the
		// task also has hidden tests (evaluating live mode on fixed issues),
		// they judge the code part of the patch, and the self-test verdict
		// is recorded beside it: do the two agree?
		st, err := verify.SelfTest(cfg.Verify, task, repoDir, patchPath, filepath.Join(outDir, "selftest"))
		if err != nil {
			return nil, fmt.Errorf("self-test: %w", err)
		}
		sum.SelfTest, sum.SelfTestReasons = st.Verdict, st.Reasons
		sum.Verdict, sum.Reasons = st.Verdict, st.Reasons
		if task.TestPatch != "" {
			_, code, _ := verify.SplitNewTests(patch, task.NewTestsDir, repoDir)
			cp := filepath.Join(outDir, "final-code.patch")
			if err := os.WriteFile(cp, []byte(code), 0o644); err != nil {
				return nil, err
			}
			if code == "" {
				sum.Verdict, sum.Reasons = verify.Fail, []string{"agent changed no code (only added tests)"}
			} else {
				ev, err := verify.Run(cfg.Verify, task, repoDir, cp, filepath.Join(outDir, "final"))
				if err != nil {
					return nil, fmt.Errorf("final verify: %w", err)
				}
				sum.Verdict, sum.Reasons = ev.Verdict, ev.Reasons
			}
		}
	default:
		ev, err := verify.Run(cfg.Verify, task, repoDir, patchPath, filepath.Join(outDir, "final"))
		if err != nil {
			return nil, fmt.Errorf("final verify: %w", err)
		}
		sum.Verdict, sum.Reasons = ev.Verdict, ev.Reasons
	}
	sum.WallMS = time.Since(start).Milliseconds()
	b, _ := json.MarshalIndent(sum, "", "  ")
	return sum, os.WriteFile(filepath.Join(outDir, "summary.json"), append(b, '\n'), 0o644)
}

// estimateTokens: see model.EstimateTokens.
func estimateTokens(msgs []model.Message) int { return model.EstimateTokens(msgs) }

func testOutput(dir string, ev *verify.Evidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "verdict: %s (%s)\n", ev.Verdict, strings.Join(ev.Reasons, "; "))
	for _, f := range []string{"stdout.log", "stderr.log"} {
		raw, _ := os.ReadFile(filepath.Join(dir, f))
		b.WriteString(tail(string(raw), 60))
	}
	return b.String()
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append([]string{fmt.Sprintf("... (%d lines omitted)", len(lines)-n)}, lines[len(lines)-n:]...)
	}
	return strings.Join(lines, "\n") + "\n"
}

// workspace is the model's only view of the repository.
type workspace struct {
	root, orig string
	protect    []string
	strict     bool   // see Config.StrictEdits
	newTests   string // live mode: where new regression tests may be created
}

var errRefused = errors.New("refused")

// resolve maps a model-supplied path to a regular file or directory inside root.
func (w *workspace) resolve(p string) (string, string, error) {
	rel := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(p, "./")))
	if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", "", fmt.Errorf("%w: path %q is outside the repository", errRefused, p)
	}
	full := filepath.Join(w.root, rel)
	if fi, err := os.Lstat(full); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return "", "", fmt.Errorf("%w: %q is a symlink", errRefused, p)
	}
	return full, rel, nil
}

func (w *workspace) protected(rel string) bool {
	for _, pre := range w.protect {
		if rel == strings.TrimSuffix(pre, "/") || strings.HasPrefix(rel, pre) {
			return true
		}
	}
	return false
}

func (w *workspace) call(name string, args map[string]any) (string, bool) {
	str := func(k string) string { s, _ := args[k].(string); return s }
	var out string
	var err error
	switch name {
	case "list_dir":
		out, err = w.listDir(str("path"))
	case "search":
		out, err = w.search(str("pattern"), str("path"))
	case "read_file":
		out, err = w.readFile(str("path"), num(args, "start_line"), num(args, "end_line"))
	case "replace_in_file":
		out, err = w.replace(str("path"), str("old_text"), str("new_text"))
	case "replace_lines":
		out, err = w.replaceLines(str("path"), num(args, "start_line"), num(args, "end_line"), str("new_text"))
	case "create_file":
		if w.newTests == "" {
			err = fmt.Errorf("%w: unknown tool %q", errRefused, name)
			break
		}
		out, err = w.createFile(str("path"), str("content"))
	default:
		err = fmt.Errorf("%w: unknown tool %q", errRefused, name)
	}
	if err != nil {
		return "error: " + err.Error(), errors.Is(err, errRefused)
	}
	return out, false
}

// isEdit reports whether a tool changes the working copy.
func isEdit(name string) bool {
	return name == "replace_in_file" || name == "replace_lines" || name == "create_file"
}

func num(args map[string]any, k string) int {
	switch v := args[k].(type) {
	case float64:
		return int(v)
	case string:
		var n int
		fmt.Sscan(v, &n)
		return n
	}
	return 0
}

const (
	maxListEntries = 400
	maxSearchHits  = 50
	maxReadLines   = 300
	// stuckAfter ends a run whose last stuckAfter tool calls were the same
	// failing call: by then the model is not going to change course.
	stuckAfter = 6
	// stuckIdentical ends a run that has made the same read-only call this
	// many times since its last edit (see NIGHTLOG, 0.6).
	stuckIdentical = 5
)

func skipDir(name string) bool {
	return name == ".git" || name == "__pycache__" || name == "node_modules" || name == ".tox"
}

// listDir lists one directory, directories first, marked with a slash.
func (w *workspace) listDir(p string) (string, error) {
	if p == "" || p == "." || p == "/" {
		p = "."
	}
	dir := w.root
	if p != "." {
		full, _, err := w.resolve(p)
		if err != nil {
			return "", err
		}
		dir = full
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var dirs, files []string
	for _, e := range entries {
		if skipDir(e.Name()) {
			continue
		}
		rel, _ := filepath.Rel(w.root, filepath.Join(dir, e.Name()))
		rel = filepath.ToSlash(rel)
		if e.IsDir() {
			dirs = append(dirs, rel+"/")
		} else if w.protected(rel) {
			files = append(files, rel+"  (protected)")
		} else {
			files = append(files, rel)
		}
	}
	all := append(dirs, files...)
	note := ""
	if len(all) > maxListEntries {
		note = fmt.Sprintf("\n[%d more entries not shown]", len(all)-maxListEntries)
		all = all[:maxListEntries]
	}
	if len(all) == 0 {
		return "(empty directory)", nil
	}
	return strings.Join(all, "\n") + note, nil
}

// search finds lines matching a regular expression (RE2 syntax) in text files
// under path.
func (w *workspace) search(pattern, p string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("bad pattern: %v", err)
	}
	root := w.root
	if p != "" && p != "." {
		full, _, err := w.resolve(p)
		if err != nil {
			return "", err
		}
		root = full
	}
	var hits []string
	total := 0
	err = filepath.WalkDir(root, func(fp string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 1<<20 || !d.Type().IsRegular() {
			return nil
		}
		raw, err := os.ReadFile(fp)
		if err != nil || bytes.IndexByte(raw, 0) >= 0 {
			return nil // unreadable or binary
		}
		rel, _ := filepath.Rel(w.root, fp)
		for i, line := range strings.Split(string(raw), "\n") {
			if re.MatchString(line) {
				total++
				if len(hits) < maxSearchHits {
					if len(line) > 200 {
						line = line[:200] + "..."
					}
					hits = append(hits, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), i+1, strings.TrimRight(line, "\r")))
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if total == 0 {
		return "no matches", nil
	}
	out := strings.Join(hits, "\n")
	if total > len(hits) {
		out += fmt.Sprintf("\n[%d matches in total; %d shown. Narrow the pattern or the path.]", total, len(hits))
	}
	return out, nil
}

// readFile shows up to maxReadLines lines, numbered, from start (1-based).
func (w *workspace) readFile(p string, start, end int) (string, error) {
	full, _, err := w.resolve(p)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(raw), "\n")
	if start < 1 {
		start = 1
	}
	if end < start || end > start+maxReadLines-1 {
		end = start + maxReadLines - 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > len(lines) {
		return "", fmt.Errorf("file has %d lines", len(lines))
	}
	var b strings.Builder
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%4d| %s\n", i, lines[i-1])
	}
	if start > 1 || end < len(lines) {
		fmt.Fprintf(&b, "[lines %d-%d of %d]\n", start, end, len(lines))
	}
	return b.String(), nil
}

func (w *workspace) replace(p, oldText, newText string) (string, error) {
	full, rel, err := w.resolve(p)
	if err != nil {
		return "", err
	}
	if w.protected(rel) {
		return "", fmt.Errorf("%w: %s is protected; fix the code, not the tests", errRefused, rel)
	}
	if oldText == "" {
		return "", errors.New("old_text is empty")
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	s := string(raw)
	note := ""
	switch n := strings.Count(s, oldText); {
	case n == 0 && w.strict:
		return "", errors.New("old_text not found; read the file and copy the text exactly, without line numbers")
	case n == 1:
		s = strings.Replace(s, oldText, newText, 1)
	case n == 0:
		// Models often get the indentation of a block wrong. Accept a block
		// whose lines match once the indentation is shifted uniformly, and
		// shift new_text by the same amount.
		out, indent, matches := replaceReindented(s, oldText, newText)
		switch matches {
		case 1:
			s, note = out, fmt.Sprintf(" (matched with indentation adjusted to %d spaces)", len(indent))
		case 0:
			return "", fmt.Errorf("old_text not found; copy it exactly from read_file, without line numbers%s", nearest(s, oldText))
		default:
			return "", fmt.Errorf("old_text matches %d places once indentation is ignored; include more surrounding lines", matches)
		}
	default:
		return "", fmt.Errorf("old_text matches %d places; include more surrounding lines", n)
	}
	if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
		return "", err
	}
	if w.strict {
		return "ok: replaced 1 occurrence in " + rel, nil
	}
	// Show the result so the model can see what the file now looks like.
	return fmt.Sprintf("ok: replaced 1 occurrence in %s%s\n%s", rel, note, around(s, newText, 3)), nil
}

// createFile creates a new file; it never overwrites. Under a protected path
// only a live-mode regression test may be created: a new test_brokkr_*.py
// file under the task's new-tests directory. Existing tests stay read-only.
func (w *workspace) createFile(p, content string) (string, error) {
	full, rel, err := w.resolve(p)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(full); err == nil {
		return "", fmt.Errorf("%w: %s already exists; create_file makes new files only (edit existing ones with replace_in_file)", errRefused, rel)
	}
	if w.protected(rel) && !verify.IsNewTestFile(w.newTests, rel, w.orig) {
		return "", fmt.Errorf("%w: %s is protected; new tests go in a new %stest_brokkr_<name>.py file", errRefused, rel, w.newTests)
	}
	// Parent directories must not escape the repository through a symlink.
	for d := filepath.Dir(rel); d != "." && d != "/"; d = filepath.Dir(d) {
		if fi, err := os.Lstat(filepath.Join(w.root, d)); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: %s is a symlink", errRefused, d)
		}
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("ok: created %s (%d lines)", rel, strings.Count(content, "\n")+1), nil
}

// newTestFiles lists the regression tests created so far, for run_tests.
func (w *workspace) newTestFiles() []string {
	if w.newTests == "" {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(filepath.Join(w.root, w.newTests), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(w.root, p)
		rel = filepath.ToSlash(rel)
		if verify.IsNewTestFile(w.newTests, rel, w.orig) {
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// syntaxError parses a Python file after an edit and returns the parser's
// message, or "" if it parses, is not Python, or no parser is available.
// ast.parse only parses: nothing in the repository is executed on the host.
// The interpreter here may be newer than the repository's, so a message is a
// warning for the model, never a verdict.
func (w *workspace) syntaxError(p string) string {
	if !strings.HasSuffix(p, ".py") {
		return ""
	}
	full, _, err := w.resolve(p)
	if err != nil {
		return ""
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, "-I", "-c",
		"import ast,sys\ntry:\n ast.parse(open(sys.argv[1],'rb').read(), sys.argv[1])\nexcept SyntaxError as e:\n print(f'{e.msg} (line {e.lineno})')", full)
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

// replaceLines replaces lines start..end (1-based, inclusive) with newText.
// Models read line numbers reliably from read_file; exact-text matching is
// where they fail most (see NIGHTLOG, 0.5).
func (w *workspace) replaceLines(p string, start, end int, newText string) (string, error) {
	full, rel, err := w.resolve(p)
	if err != nil {
		return "", err
	}
	if w.protected(rel) {
		return "", fmt.Errorf("%w: %s is protected; fix the code, not the tests", errRefused, rel)
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(raw), "\n")
	if start < 1 || end < start || end > len(lines) {
		return "", fmt.Errorf("line range %d-%d is outside the file (1-%d)", start, end, len(lines))
	}
	repl := strings.Split(strings.TrimSuffix(newText, "\n"), "\n")
	if newText == "" {
		repl = nil // delete the lines
	}
	out := append(append(append([]string{}, lines[:start-1]...), repl...), lines[end:]...)
	s := strings.Join(out, "\n")
	if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
		return "", err
	}
	lo, hi := max(1, start-3), min(len(out), start+len(repl)+2)
	var b strings.Builder
	fmt.Fprintf(&b, "ok: replaced lines %d-%d of %s with %d line(s). The file now reads:\n", start, end, rel, len(repl))
	for i := lo; i <= hi; i++ {
		fmt.Fprintf(&b, "%4d| %s\n", i, out[i-1])
	}
	return b.String(), nil
}

// replaceReindented finds blocks in s equal to oldText up to one uniform
// indentation shift and, if there is exactly one, replaces it with newText
// shifted the same way.
func replaceReindented(s, oldText, newText string) (string, string, int) {
	oldLines := dedent(strings.Split(strings.TrimRight(oldText, "\n"), "\n"))
	lines := strings.Split(s, "\n")
	var at int
	var indent string
	matches := 0
	for i := 0; i+len(oldLines) <= len(lines); i++ {
		ind, ok := "", true
		first := true
		for j, ol := range oldLines {
			fl := lines[i+j]
			if strings.TrimSpace(ol) == "" {
				if strings.TrimSpace(fl) != "" {
					ok = false
					break
				}
				continue
			}
			if !strings.HasSuffix(fl, ol) {
				ok = false
				break
			}
			pre := fl[:len(fl)-len(ol)]
			if strings.TrimSpace(pre) != "" || (!first && pre != ind) {
				ok = false
				break
			}
			ind, first = pre, false
		}
		if ok && !first {
			matches++
			at, indent = i, ind
		}
	}
	if matches != 1 {
		return "", "", matches
	}
	newLines := dedent(strings.Split(strings.TrimRight(newText, "\n"), "\n"))
	for k, l := range newLines {
		if strings.TrimSpace(l) != "" {
			newLines[k] = indent + l
		}
	}
	out := append(append(append([]string{}, lines[:at]...), newLines...), lines[at+len(oldLines):]...)
	return strings.Join(out, "\n"), indent, 1
}

func dedent(lines []string) []string {
	common := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if common < 0 || n < common {
			common = n
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		switch {
		case common <= 0:
			out[i] = l
		case strings.TrimSpace(l) == "":
			out[i] = ""
		default:
			out[i] = l[common:]
		}
	}
	return out
}

// nearest points at lines resembling the first line of text, as a hint.
func nearest(s, text string) string {
	want := ""
	for _, l := range strings.Split(text, "\n") {
		if want = strings.TrimSpace(l); want != "" {
			break
		}
	}
	var hits []string
	for i, l := range strings.Split(s, "\n") {
		if want != "" && strings.Contains(l, want) {
			hits = append(hits, fmt.Sprintf("%4d| %s", i+1, l))
		}
	}
	if len(hits) == 0 || len(hits) > 5 {
		return ""
	}
	return ". Lines containing its first line:\n" + strings.Join(hits, "\n")
}

// around returns the lines surrounding the first line of text in s, numbered.
func around(s, text string, ctx int) string {
	lines := strings.Split(s, "\n")
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	idx := 0
	for i, l := range lines {
		if first != "" && strings.Contains(l, first) {
			idx = i
			break
		}
	}
	n := strings.Count(strings.TrimRight(text, "\n"), "\n") + 1
	lo, hi := max(0, idx-ctx), min(len(lines), idx+n+ctx)
	var b strings.Builder
	for i := lo; i < hi; i++ {
		fmt.Fprintf(&b, "%4d| %s\n", i+1, lines[i])
	}
	return b.String()
}

// runTests verifies the current diff in a fresh microVM and returns the output
// shown to the model plus the verdict. The caller has checked the diff is non-empty.
func (w *workspace) runTests(vc verify.Config, task verify.Task, dir string) (string, verify.Verdict) {
	patch, err := w.diff()
	if err != nil {
		return "error: " + err.Error(), verify.Error
	}
	pp := filepath.Join(dir + ".patch")
	if err := os.WriteFile(pp, []byte(patch), 0o644); err != nil {
		return "error: " + err.Error(), verify.Error
	}
	if task.Live {
		// The existing tests for the area plus the agent's new tests,
		// statuses only. Protection is the workspace's job here (the new
		// files sit under a protected directory); the final self-test
		// checks it again.
		task.TestPatch, task.RequiredTests, task.Protect = "", nil, nil
		task.TestCmd = strings.TrimSpace(task.TestCmd + " " + strings.Join(w.newTestFiles(), " "))
		vc.CollectOnly = true
		ev, err := verify.Run(vc, task, w.orig, pp, dir)
		if err != nil {
			return "error: " + err.Error(), verify.Error
		}
		return fmt.Sprintf("(existing tests plus your new tests; %d passed, %d failed)\n", len(ev.Tests.Passed), len(ev.Tests.Failed)) + testOutput(dir, ev), verify.Verdict("VISIBLE_" + string(ev.Verdict))
	}
	if task.TestPatch != "" {
		// The hidden tests stay hidden: run the repository's own tests for
		// the affected area, judged by exit code alone.
		task.TestPatch, task.RequiredTests = "", nil
		ev, err := verify.Run(vc, task, w.orig, pp, dir)
		if err != nil {
			return "error: " + err.Error(), verify.Error
		}
		return "(existing tests only; the hidden tests are not run)\n" + testOutput(dir, ev), verify.Verdict("VISIBLE_" + string(ev.Verdict))
	}
	ev, err := verify.Run(vc, task, w.orig, pp, dir)
	if err != nil {
		return "error: " + err.Error(), verify.Error
	}
	return testOutput(dir, ev), ev.Verdict
}

// runPython runs code with python3 from the repository root in a fresh
// microVM, on the current working copy. The code is passed on stdin, so it
// never becomes part of the diff.
func (w *workspace) runPython(vc verify.Config, task verify.Task, code, dir string) string {
	patch, err := w.diff()
	if err != nil {
		return "error: " + err.Error()
	}
	pp := ""
	if patch != "" {
		pp = dir + ".patch"
		if err := os.WriteFile(pp, []byte(patch), 0o644); err != nil {
			return "error: " + err.Error()
		}
	}
	delim := "BROKKR_PY_EOF"
	for strings.Contains(code, delim) {
		delim += "_"
	}
	cmd := fmt.Sprintf("PYTHONPATH=$PWD python3 - <<'%s'\n%s\n%s", delim, code, delim)
	res, err := verify.Exec(vc, task, w.orig, pp, cmd, 120, dir)
	if err != nil {
		return "error: " + err.Error()
	}
	out := fmt.Sprintf("exit code %d", res.ExitCode)
	if res.TimedOut {
		out += " (timed out after 120s)"
	}
	if s := strings.TrimSpace(res.Stdout); s != "" {
		out += "\nstdout:\n" + tail(s, 80)
	}
	if s := strings.TrimSpace(res.Stderr); s != "" {
		out += "\nstderr:\n" + tail(s, 80)
	}
	return out
}

// compactor shortens old tool outputs in batches. When the conversation's
// estimated size passes High, every tool output except the newest Keep is
// shortened, and nothing changes again until the size passes High once more.
// Batching keeps the prompt's prefix stable between batches, so a server's
// prompt cache (Ollama's, for one) stays valid; shortening a little every turn
// would invalidate it every turn. The transcript on disk keeps everything.
type compactor struct {
	High      int
	Keep      int
	shortened map[int]bool
	Batches   int
}

func (c *compactor) view(msgs []model.Message) []model.Message {
	if c.shortened == nil {
		c.shortened = map[int]bool{}
	}
	build := func() []model.Message {
		out := make([]model.Message, len(msgs))
		copy(out, msgs)
		for i := range out {
			if c.shortened[i] && len(out[i].Content) > 400 {
				out[i].Content = out[i].Content[:400] + "\n[... older output shortened by brokkr; call the tool again if you need it]"
			}
		}
		return out
	}
	v := build()
	if c.High <= 0 || estimateTokens(v) <= c.High {
		return v
	}
	seen := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "tool" {
			seen++
			if seen > c.Keep {
				c.shortened[i] = true
			}
		}
	}
	c.Batches++
	return build()
}

// diff returns a unified diff from the pristine copy to the working copy,
// applicable with `git apply` (paths are a/... and b/...).
func (w *workspace) diff() (string, error) {
	cmd := exec.Command("diff", "-ruN", "-x", "__pycache__", "-x", ".git", "a", "b")
	cmd.Dir = filepath.Dir(w.root)
	out, err := cmd.Output()
	var ee *exec.ExitError
	if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 1) {
		return "", fmt.Errorf("diff: %w", err)
	}
	return string(out), nil
}

func toolDefs(live bool) []model.Tool {
	obj := func(props map[string]any, req ...string) map[string]any {
		if props == nil {
			props = map[string]any{}
		}
		if req == nil {
			req = []string{}
		}
		return map[string]any{"type": "object", "properties": props, "required": req}
	}
	s := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	t := func(name, desc string, params map[string]any) model.Tool {
		return model.Tool{Type: "function", Function: model.ToolFunction{Name: name, Description: desc, Parameters: params}}
	}
	tools := []model.Tool{
		t("list_dir", "List one directory (not recursive). Directories end with a slash.",
			obj(map[string]any{"path": s("directory relative to the repository root; '.' for the root")})),
		t("search", "Search files for a regular expression (RE2 syntax). Returns path:line: text for up to 50 matches.",
			obj(map[string]any{
				"pattern": s("regular expression, e.g. 'def parse_header' or 'class .*Error'"),
				"path":    s("file or directory to search in; default is the whole repository"),
			}, "pattern")),
		t("read_file", "Read a file, 300 lines at most per call. Lines are shown with numbers, which are not part of the file.",
			obj(map[string]any{
				"path":       s("path relative to the repository root"),
				"start_line": map[string]any{"type": "integer", "description": "first line to show, default 1"},
				"end_line":   map[string]any{"type": "integer", "description": "last line to show"},
			}, "path")),
		t("replace_in_file", "Replace one exact occurrence of old_text with new_text in a file.",
			obj(map[string]any{
				"path":     s("path relative to the repository root"),
				"old_text": s("exact text currently in the file, without line numbers"),
				"new_text": s("replacement text"),
			}, "path", "old_text", "new_text")),
		t("replace_lines", "Replace lines start_line..end_line (inclusive, as numbered by read_file) with new_text. Use it when replace_in_file cannot match the text. Read the lines again after editing: numbers shift.",
			obj(map[string]any{
				"path":       s("path relative to the repository root"),
				"start_line": map[string]any{"type": "integer"},
				"end_line":   map[string]any{"type": "integer"},
				"new_text":   s("the replacement lines, without line numbers; empty deletes the lines"),
			}, "path", "start_line", "end_line", "new_text")),
		t("run_tests", "Run the tests on your current changes in a sandbox and return the output.", obj(nil)),
		t("run_python", "Run Python code with python3 from the repository root in a sandbox, on your current changes. Use it to reproduce the issue and to check your fix. Print what you want to see.",
			obj(map[string]any{"code": s("a complete Python program")}, "code")),
		t("submit", "Finish. Set fixed=true if the tests pass with your change.",
			obj(map[string]any{
				"fixed":   map[string]any{"type": "boolean"},
				"summary": s("one sentence on what you changed"),
			}, "fixed")),
	}
	if live {
		// After replace_lines, so the edit tools sit together.
		tools = append(tools[:5:5], append([]model.Tool{
			t("create_file", "Create a new file (never overwrites). Use it for your regression test: a new test_brokkr_<name>.py in the tests directory named in the task.",
				obj(map[string]any{
					"path":    s("path relative to the repository root"),
					"content": s("the complete file content"),
				}, "path", "content")),
		}, tools[5:]...)...)
	}
	return tools
}
