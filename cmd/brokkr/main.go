// Command brokkr is the Brokkr control plane.
//
//	brokkr verify --task T.json --repo DIR [--patch P.patch] --out DIR
//	brokkr fix    --task T.json --repo DIR --out DIR [--model NAME] [--model-url URL]
//	brokkr selftest --task T.json --repo DIR --patch P --out DIR   (live tasks)
//	brokkr route  --routes dev/routes.json --for owner/repo   (which backend, and why)
//
// Exit status: 0 PASS, 1 FAIL, 3 REJECTED, 2 ERROR or bad usage,
// 4 free-tier budget parked (stop the batch; resume after parked_until).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/4ktLuffy/brokkr/internal/agent"
	"github.com/4ktLuffy/brokkr/internal/bundle"
	"github.com/4ktLuffy/brokkr/internal/model"
	"github.com/4ktLuffy/brokkr/internal/route"
	"github.com/4ktLuffy/brokkr/internal/verify"
)

const usage = `usage:
  brokkr verify --task T.json --repo DIR [--patch P] --out DIR
  brokkr fix    --task T.json --repo DIR --out DIR [--model NAME] [--model-url URL]
  brokkr selftest --task T.json --repo DIR --patch P --out DIR
  brokkr route  --routes R.json --for owner/repo [--results GLOB]
  brokkr bundle --run RUN_DIR --out DIR`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if os.Args[1] == "bundle" {
		bundleCmd(os.Args[2:])
		return
	}
	if os.Args[1] == "route" {
		routeCmd(os.Args[2:])
		return
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	taskPath := fs.String("task", "", "task spec (JSON)")
	repo := fs.String("repo", "", "repository (never modified)")
	out := fs.String("out", "", "directory for evidence and logs")
	runner := fs.String("runner", envOr("BROKKR_RUNNER", "brokkr-runner"), "path to brokkr-runner")
	host := fs.String("host", envOr("BROKKR_HOST", hostLabel()), "machine label recorded in the evidence")
	var patch, modelName, modelURL *string
	var collect *bool
	var maxTurns, maxTests, ctxTokens, maxReply, compactAbove *int
	var strict *bool
	var temp, topP, presence *float64
	var reasoning, routes, results *string
	switch os.Args[1] {
	case "verify", "selftest":
		patch = fs.String("patch", "", "unified diff to verify; omit to verify the unpatched repo")
		collect = fs.Bool("collect-only", false, "run and record per-test statuses, give no verdict (for deriving required tests)")
	case "fix":
		modelName = fs.String("model", envOr("BROKKR_MODEL", "brokkr-qwen2.5-7b-16k"), "model name")
		modelURL = fs.String("model-url", envOr("BROKKR_MODEL_URL", "http://host.lima.internal:11434/v1"), "OpenAI-compatible base URL")
		maxTurns = fs.Int("max-turns", envInt("BROKKR_MAX_TURNS", 20), "model calls before giving up")
		maxTests = fs.Int("max-test-runs", envInt("BROKKR_MAX_SANDBOX_RUNS", 5), "sandbox runs (run_tests + run_python) the agent may request")
		compactAbove = fs.Int("compact-above", envInt("BROKKR_COMPACT_ABOVE", 16000), "estimated prompt tokens above which older tool outputs are shortened; 0 never")
		ctxTokens = fs.Int("context-tokens", envInt("BROKKR_CONTEXT_TOKENS", 16384), "model context window as served; 0 disables the overflow check")
		maxReply = fs.Int("max-reply-tokens", envInt("BROKKR_MAX_REPLY_TOKENS", 2048), "cap on each model reply (thinking counts toward it)")
		strict = fs.Bool("strict-edits", os.Getenv("BROKKR_STRICT_EDITS") == "1", "exact-match edits only (ablation)")
		temp = fs.Float64("temperature", envFloat("BROKKR_TEMPERATURE", 0), "sampling temperature")
		topP = fs.Float64("top-p", envFloat("BROKKR_TOP_P", 0), "nucleus sampling; 0 leaves the server default")
		presence = fs.Float64("presence-penalty", envFloat("BROKKR_PRESENCE_PENALTY", 0), "0 leaves the server default")
		reasoning = fs.String("reasoning-effort", os.Getenv("BROKKR_REASONING_EFFORT"), `sent as reasoning_effort when set; "none" disables thinking`)
		routes = fs.String("routes", os.Getenv("BROKKR_ROUTES"), "routes file: pick and fall back between backends (the model flags are then ignored)")
		results = fs.String("results", envOr("BROKKR_RESULTS", "results/*/runs.jsonl"), "past runs the router learns from")
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	_ = fs.Parse(os.Args[2:])
	if *taskPath == "" || *repo == "" || *out == "" {
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
	// Paths inside a task file are relative to the task file.
	for _, p := range []*string{&task.TestPatch, &task.EnvImage} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(filepath.Dir(*taskPath), *p)
		}
	}
	vc := verify.Config{Runner: *runner, Host: *host}

	var v verify.Verdict
	var reasons []string
	if os.Args[1] == "selftest" {
		// A patch carrying its own regression test, judged as live mode
		// judges an agent's: fails before, passes after, breaks nothing.
		if *patch == "" || task.NewTestsDir == "" {
			die(fmt.Errorf("selftest needs --patch and a task with new_tests_dir"))
		}
		st, err := verify.SelfTest(vc, task, *repo, *patch, *out)
		if err != nil {
			die(err)
		}
		v, reasons = st.Verdict, st.Reasons
	} else if os.Args[1] == "verify" {
		vc.CollectOnly = *collect
		ev, err := verify.Run(vc, task, *repo, *patch, *out)
		if err != nil {
			die(err)
		}
		v, reasons = ev.Verdict, ev.Reasons
	} else {
		client := &model.Client{BaseURL: *modelURL, Model: *modelName, APIKey: os.Getenv("BROKKR_API_KEY"), MaxTokens: *maxReply,
			Temperature: *temp, TopP: *topP, PresencePenalty: *presence, ReasoningEffort: *reasoning}
		var router *route.Router
		if *routes != "" {
			ranked, err := rank(*routes, *results, route.RepoOf(task.Name))
			if err != nil {
				die(err)
			}
			order := route.Order(ranked)
			if len(order) == 0 {
				die(fmt.Errorf("routes: no backend is up"))
			}
			fmt.Fprintf(os.Stderr, "route: %s\n", describe(ranked))
			router = route.New(order)
			client = router.Primary().Client()
			*ctxTokens = router.Primary().ContextTokens
		}
		sum, err := agent.Run(context.Background(), agent.Config{
			Model: client, Router: router, Verify: vc, MaxTurns: *maxTurns, MaxTestRuns: *maxTests,
			ContextTokens: *ctxTokens, StrictEdits: *strict, CompactAbove: *compactAbove,
		}, task, *repo, *out)
		if err != nil {
			die(err)
		}
		v = sum.Verdict
		reasons = append(sum.Reasons, fmt.Sprintf("claimed=%v turns=%d test_runs=%d max_prompt=%d stop=%q",
			sum.AgentClaimed, sum.Turns, sum.TestRuns, sum.MaxPrompt, sum.StopReason))
		if sum.Infra != "" {
			v = verify.Error
			reasons = append(reasons, "INFRA: "+sum.Infra)
		}
		if sum.ParkedUntil != "" {
			fmt.Printf("PARKED  free-tier allowance exhausted; resume after %s\n", sum.ParkedUntil)
			os.Exit(4)
		}
	}
	fmt.Printf("%s  %s\n", v, strings.Join(reasons, "; "))
	switch v {
	case verify.Pass:
		os.Exit(0)
	case verify.Fail:
		os.Exit(1)
	case verify.Rejected:
		os.Exit(3)
	case verify.Collected:
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

// bundleCmd writes a review bundle for a finished run. It never pushes: it
// prints the command a human would run to open the pull request.
func bundleCmd(argv []string) {
	fs := flag.NewFlagSet("bundle", flag.ExitOnError)
	run := fs.String("run", "", "a `brokkr fix` output directory")
	out := fs.String("out", "", "where to write patch.diff and pr.md")
	_ = fs.Parse(argv)
	if *run == "" || *out == "" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	res, err := bundle.Write(*run, *out)
	if err != nil {
		die(err)
	}
	state := "READY"
	if !res.Ready {
		state = "NOT READY"
	}
	fmt.Printf("%s  %s\n  %s\n  %s\n", state, res.Title, res.PatchPath, res.BodyPath)
	if res.Ready {
		fmt.Printf("to propose it (a human decision): git apply %s && gh pr create --draft --title %q --body-file %s\n",
			res.PatchPath, res.Title, res.BodyPath)
	}
	if !res.Ready {
		os.Exit(1)
	}
}

// rank loads the routes and past results, checks which backends answer, and
// orders them for repo.
func rank(routesPath, results, repo string) ([]route.Ranked, error) {
	cfg, err := route.Load(routesPath)
	if err != nil {
		return nil, err
	}
	st, _, err := route.LoadStats(results)
	if err != nil {
		return nil, err
	}
	down := map[string]string{}
	for _, b := range cfg.Backends {
		if err := route.Up(context.Background(), b, 5*time.Second); err != nil {
			down[b.Name] = err.Error()
		}
	}
	return route.Pick(cfg, st, repo, down), nil
}

func describe(r []route.Ranked) string {
	var parts []string
	for _, x := range r {
		s := fmt.Sprintf("%s %d/%d", x.Backend.Name, x.Stat.Pass, x.Stat.Scored)
		if x.Down != "" {
			s += " (down)"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " > ")
}

// routeCmd shows which backend a task would get, and the evidence.
func routeCmd(argv []string) {
	fs := flag.NewFlagSet("route", flag.ExitOnError)
	routes := fs.String("routes", envOr("BROKKR_ROUTES", "dev/routes.json"), "routes file")
	repo := fs.String("for", "", "repository, owner/repo (or a task name)")
	results := fs.String("results", envOr("BROKKR_RESULTS", "results/*/runs.jsonl"), "past runs")
	_ = fs.Parse(argv)
	if *repo == "" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if !strings.Contains(*repo, "/") {
		*repo = route.RepoOf(*repo)
	}
	ranked, err := rank(*routes, *results, *repo)
	if err != nil {
		die(err)
	}
	fmt.Printf("%s\n\n%-12s %-24s %8s %9s %9s %10s %8s  %s\n", *repo, "backend", "model", "context", "here", "all repos", "est. rate", "per hour", "status")
	for _, x := range ranked {
		status := "up"
		if x.Down != "" {
			status = "down: " + x.Down
		}
		fmt.Printf("%-12s %-24s %8d %4d/%-4d %4d/%-4d %9.0f%% %8.2f  %s\n", x.Backend.Name, x.Backend.Model, x.Backend.ContextTokens,
			x.Stat.Pass, x.Stat.Scored, x.Overall.Pass, x.Overall.Scored, 100*x.Rate, x.PerHour, status)
	}
	fmt.Printf("\norder: %s\n(est. rate: this repo's record shrunk toward the backend's record on all real repos; for routing, not an evaluation)\n", describe(ranked))
}

func hostLabel() string {
	h, _ := os.Hostname()
	return h
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envFloat(k string, def float64) float64 {
	var f float64
	if _, err := fmt.Sscan(os.Getenv(k), &f); err == nil {
		return f
	}
	return def
}

func envInt(k string, def int) int {
	var n int
	if _, err := fmt.Sscan(os.Getenv(k), &n); err == nil {
		return n
	}
	return def
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "brokkr:", err)
	os.Exit(2)
}
