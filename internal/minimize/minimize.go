// Package minimize shrinks a patch that verifies to the smallest one that still
// does, using the real verifier as the oracle (delta debugging over hunks, then
// optionally over changed lines).
package minimize

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Oracle judges one candidate patch (empty text means no patch at all). It
// returns the verdict and the id of the verifier run behind it.
type Oracle func(patch string) (verdict, runID string, err error)

type Config struct {
	Oracle Oracle
	// Applies reports whether a candidate still applies to the repository. A
	// candidate that does not counts as FAIL without calling the oracle.
	Applies     func(patch string) error
	Granularity string // "hunk" (default) or "line"
	MaxRuns     int    // oracle calls allowed; 0 is no limit
}

// Run is one judged candidate, as recorded in minimize.json.
type Run struct {
	N       int    `json:"n"`
	Phase   string `json:"phase"` // full, hunk or line
	Kept    []int  `json:"kept"`  // unit ids in the candidate
	SHA256  string `json:"patch_sha256"`
	Verdict string `json:"verdict"` // PASS, FAIL, ERROR, or NOAPPLY (never booted)
	RunID   string `json:"run_id,omitempty"`
	Note    string `json:"note,omitempty"`
}

type Report struct {
	Granularity  string   `json:"granularity"`
	MinimalPatch string   `json:"-"`
	LinesBefore  int      `json:"changed_lines_before"`
	LinesAfter   int      `json:"changed_lines_after"`
	UnitsBefore  int      `json:"hunks_before"`
	UnitsAfter   int      `json:"hunks_after"`
	OracleRuns   int      `json:"oracle_runs"` // verifier runs spent
	NoApply      int      `json:"not_applicable"`
	CacheHits    int      `json:"cache_hits"`
	MaxRuns      int      `json:"max_runs"`
	OneMinimal   bool     `json:"one_minimal"`
	Stopped      string   `json:"stopped,omitempty"` // why it is not 1-minimal
	Removed      []string `json:"removed"`
	Kept         []string `json:"kept"`
	Runs         []Run    `json:"runs"`
}

// Minimize reduces patch. The whole patch must PASS first. On a budget stop or
// an oracle error it still returns the best verified patch, with the reason in
// Report.Stopped (budget) or as err (oracle).
func Minimize(patch string, cfg Config) (*Report, error) {
	if cfg.Granularity == "" {
		cfg.Granularity = "hunk"
	}
	if cfg.Granularity != "hunk" && cfg.Granularity != "line" {
		return nil, fmt.Errorf("granularity must be hunk or line, not %q", cfg.Granularity)
	}
	p, err := parse(patch)
	if err != nil {
		return nil, err
	}
	rep := &Report{Granularity: cfg.Granularity, MaxRuns: cfg.MaxRuns, UnitsBefore: len(p.units), LinesBefore: changed(patch),
		Removed: []string{}, Kept: []string{}, Runs: []Run{}}
	all := make([]int, len(p.units))
	for i := range all {
		all[i] = i
	}

	cache := map[string]bool{}
	phase := "full"
	// try judges the candidate text; kept is only recorded.
	try := func(text string, kept []int) (bool, error) {
		sum := sha256.Sum256([]byte(text))
		key := hex.EncodeToString(sum[:])
		if v, ok := cache[key]; ok {
			rep.CacheHits++
			return v, nil
		}
		r := Run{N: len(rep.Runs) + 1, Phase: phase, Kept: append([]int{}, kept...), SHA256: key}
		if text != "" && cfg.Applies != nil {
			if err := cfg.Applies(text); err != nil {
				r.Verdict, r.Note = "NOAPPLY", firstLine(err.Error())
				rep.NoApply++
				rep.Runs = append(rep.Runs, r)
				cache[key] = false
				return false, nil
			}
		}
		if cfg.MaxRuns > 0 && rep.OracleRuns >= cfg.MaxRuns {
			return false, ErrBudget
		}
		rep.OracleRuns++
		v, id, err := cfg.Oracle(text)
		r.Verdict, r.RunID = v, id
		if err != nil {
			r.Verdict, r.Note = "ERROR", err.Error()
			rep.Runs = append(rep.Runs, r)
			return false, err
		}
		rep.Runs = append(rep.Runs, r)
		cache[key] = v == "PASS"
		return v == "PASS", nil
	}

	fullKeep := keepSet(all)
	ok, err := try(p.render(fullKeep, nil), all)
	if err != nil {
		return rep, err
	}
	if !ok {
		return rep, fmt.Errorf("the full patch does not pass (%s): nothing to minimize", rep.Runs[0].Verdict)
	}

	phase = "hunk"
	keptUnits, err := ddmin(all, func(s []int) (bool, error) {
		return try(p.render(keepSet(s), nil), s)
	})
	keep := keepSet(keptUnits)
	var dropped map[lineUnit]bool
	if err == nil && cfg.Granularity == "line" {
		phase = "line"
		lus := p.changeUnits(keep)
		ids := make([]int, len(lus))
		for i := range ids {
			ids[i] = i
		}
		keptIDs, e := ddmin(ids, func(s []int) (bool, error) {
			return try(p.render(keep, droppedOf(lus, s)), s)
		})
		dropped = droppedOf(lus, keptIDs)
		err = e
	}

	rep.MinimalPatch = p.render(keep, dropped)
	rep.LinesAfter = changed(rep.MinimalPatch)
	rep.UnitsAfter = len(keptUnits)
	for i, u := range p.units {
		if keep[i] {
			rep.Kept = append(rep.Kept, p.describe(u))
		} else {
			rep.Removed = append(rep.Removed, p.describe(u))
		}
	}
	for lu := range dropped {
		rep.Removed = append(rep.Removed, p.describeLine(lu))
	}
	sort.Strings(rep.Removed)
	switch err {
	case nil:
		rep.OneMinimal = true
	case ErrBudget:
		rep.Stopped = fmt.Sprintf("run budget of %d exhausted: the result passes but may not be 1-minimal", cfg.MaxRuns)
		err = nil
	}
	return rep, err
}

func keepSet(ids []int) map[int]bool {
	m := make(map[int]bool, len(ids))
	for _, i := range ids {
		m[i] = true
	}
	return m
}

// droppedOf returns the lines of lus not in kept.
func droppedOf(lus []lineUnit, kept []int) map[lineUnit]bool {
	k := keepSet(kept)
	d := map[lineUnit]bool{}
	for i, lu := range lus {
		if !k[i] {
			d[lu] = true
		}
	}
	return d
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// GitApplies checks a candidate with git apply --check in repoDir, the same
// application the verifier does, so a subset that cannot apply is never booted.
func GitApplies(repoDir string) func(string) error {
	return func(patch string) error {
		c := exec.Command("git", "apply", "--check", "--whitespace=nowarn", "-")
		c.Dir = repoDir
		c.Stdin = strings.NewReader(patch)
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("%s", bytes.TrimSpace(out))
		}
		return nil
	}
}

// Summary is the short text written beside the patch.
func (r *Report) Summary() string {
	var b strings.Builder
	state := "1-minimal"
	if !r.OneMinimal {
		state = "NOT proven 1-minimal: " + r.Stopped
	}
	fmt.Fprintf(&b, "changed lines: %d -> %d (%s granularity, %d -> %d hunks/files)\n", r.LinesBefore, r.LinesAfter, r.Granularity, r.UnitsBefore, r.UnitsAfter)
	fmt.Fprintf(&b, "verifier runs: %d (budget %s), %d candidates did not apply, %d cache hits\n", r.OracleRuns, budget(r.MaxRuns), r.NoApply, r.CacheHits)
	fmt.Fprintf(&b, "result: %s\n", state)
	fmt.Fprintf(&b, "removed (%d):\n", len(r.Removed))
	for _, s := range r.Removed {
		fmt.Fprintf(&b, "  - %s\n", s)
	}
	return b.String()
}

func budget(n int) string {
	if n == 0 {
		return "none"
	}
	return fmt.Sprint(n)
}

// Write saves minimize.json, minimal.patch and summary.txt into dir.
func (r *Report) Write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "minimize.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.txt"), []byte(r.Summary()), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "minimal.patch"), []byte(r.MinimalPatch), 0o644)
}
