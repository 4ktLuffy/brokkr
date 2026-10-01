package mutate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Write saves mutate.json and mutate.md in dir.
func Write(dir string, res *Result) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "mutate.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "mutate.md"), []byte(Markdown(res)), 0o644)
}

// Line is the one-line score.
func (r *Result) Line() string {
	if r.Score == nil {
		return fmt.Sprintf("no score: %d mutants made, %d run, %d errored", r.Generated, r.Run, r.Errored)
	}
	s := fmt.Sprintf("%d of %d mutants killed (%.0f%%)", r.Killed, r.Killed+r.Survived, 100**r.Score)
	if r.Crashed > 0 {
		s += fmt.Sprintf("; %d of the kills only broke loading", r.Crashed)
	}
	if r.Errored > 0 {
		s += fmt.Sprintf("; %d runs errored and are not counted", r.Errored)
	}
	return s
}

// Markdown tells a reviewer what the test checks and, from the survivors, what
// it does not.
func Markdown(r *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Mutation score of the agent's test\n\n**%s.**\n\n", r.Line())
	fmt.Fprintf(&b, "Tests: %s. %d distinct mutants of the fix, %d run.\n\n", strings.Join(r.NewFiles, ", "), r.Generated, r.Run)
	b.WriteString("| Kind | Killed | Survived | Error |\n|---|---|---|---|\n")
	type c struct{ k, s, e int }
	by := map[string]*c{}
	var kinds []string
	for _, m := range r.Mutants {
		if by[m.Kind] == nil {
			by[m.Kind] = &c{}
			kinds = append(kinds, m.Kind)
		}
		switch m.Status {
		case Killed:
			by[m.Kind].k++
		case Survived:
			by[m.Kind].s++
		default:
			by[m.Kind].e++
		}
	}
	for _, k := range kinds {
		fmt.Fprintf(&b, "| %s | %d | %d | %d |\n", k, by[k].k, by[k].s, by[k].e)
	}
	surv := 0
	for _, m := range r.Mutants {
		if m.Status != Survived {
			continue
		}
		if surv == 0 {
			b.WriteString("\n### Survivors: changes to the fix that the test does not notice\n")
		}
		surv++
		fmt.Fprintf(&b, "\n**%s** `%s:%d` %s\n\n```diff\n%s```\n", m.ID, m.Path, m.Line, m.Desc, m.Delta)
	}
	for _, m := range r.Mutants {
		if m.Status == Errored {
			fmt.Fprintf(&b, "\nMutant %s (%s) did not run: %s\n", m.ID, m.Desc, m.Note)
		}
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(&b, "\nNot mutated: %s\n", strings.Join(r.Skipped, "; "))
	}
	b.WriteString("\nA surviving mutant is either a real gap in the test or an equivalent change (same behaviour); a reviewer decides which. The score says how much of the fix the test pins down, not whether the fix is right.\n")
	return b.String()
}
