package mutate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/4ktLuffy/brokkr/internal/verify"
)

// SandboxRunner runs the new test files in a fresh microVM per call, the way
// verify.SelfTest runs its run B: collect-only, no hidden tests, no required
// list, and no Protect (the new files sit under the tests directory).
type SandboxRunner struct {
	Cfg      verify.Config
	Task     verify.Task // TestCmd is used as given; see BaseCmd
	RepoDir  string
	OutDir   string
	NewFiles []string
}

func (s *SandboxRunner) Run(tag, patch string) (*Outcome, error) {
	dir := filepath.Join(s.OutDir, "runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	pp := filepath.Join(dir, tag+".patch")
	if err := os.WriteFile(pp, []byte(patch), 0o644); err != nil {
		return nil, err
	}
	t := s.Task
	t.TestPatch, t.RequiredTests, t.Protect = "", nil, nil
	t.TestCmd = strings.TrimSpace(t.TestCmd + " " + strings.Join(s.NewFiles, " "))
	cc := s.Cfg
	cc.CollectOnly = true
	ev, err := verify.Run(cc, t, s.RepoDir, pp, filepath.Join(dir, tag))
	if err != nil {
		return nil, err
	}
	if ev.Verdict != verify.Collected {
		return nil, fmt.Errorf("run %s: %s %v", tag, ev.Verdict, ev.Reasons)
	}
	out := &Outcome{}
	for _, id := range ev.Tests.Passed {
		if isNew(s.NewFiles, id) {
			out.Passed = append(out.Passed, id)
		}
	}
	for _, id := range ev.Tests.Failed {
		if isNew(s.NewFiles, id) {
			out.Failed = append(out.Failed, id)
		}
	}
	return out, nil
}

func isNew(files []string, id string) bool {
	for _, f := range files {
		if strings.HasPrefix(id, f+"::") {
			return true
		}
	}
	return false
}
