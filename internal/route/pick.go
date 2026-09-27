package route

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Stat is a backend's record on one repository.
type Stat struct {
	Scored, Pass int
	WallMS       int64 // total, scored runs only
}

// priorWeight is how many runs the prior counts as (see Pick).
const priorWeight = 4

// Stats maps model -> repository -> record.
type Stats map[string]map[string]Stat

// RepoOf names a task's repository: "django__django-11099" -> "django/django".
// Tasks that are not from a repository (the toy fixtures) are "toy".
func RepoOf(task string) string {
	head, _, ok := strings.Cut(task, "-")
	if owner, repo, found := strings.Cut(head, "__"); ok && found {
		return owner + "/" + repo
	}
	return "toy"
}

// LoadStats reads every runs.jsonl matched by pattern. Rows with no verdict
// the agent earned (ERROR, infra errors) are left out, as in the reports.
func LoadStats(pattern string) (Stats, int, error) {
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, 0, err
	}
	st := Stats{}
	rows := 0
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return nil, 0, err
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var r struct {
				Task, Model, Verdict string
				Infra                string         `json:"infra_error"`
				WallMS               int64          `json:"wall_ms"`
				ServedBy             map[string]int `json:"served_by"`
			}
			if json.Unmarshal(sc.Bytes(), &r) != nil || r.Verdict == "" || r.Verdict == "ERROR" || r.Infra != "" {
				continue
			}
			if len(r.ServedBy) > 1 {
				continue // mixed run: says nothing about one model
			}
			repo := RepoOf(r.Task)
			if st[r.Model] == nil {
				st[r.Model] = map[string]Stat{}
			}
			s := st[r.Model][repo]
			s.Scored++
			s.WallMS += r.WallMS
			if r.Verdict == "PASS" {
				s.Pass++
			}
			st[r.Model][repo] = s
			rows++
		}
		fh.Close()
		if err := sc.Err(); err != nil {
			return nil, 0, fmt.Errorf("%s: %w", f, err)
		}
	}
	return st, rows, nil
}

// Ranked is one backend's place in a Pick, with the evidence.
type Ranked struct {
	Backend Backend
	Stat    Stat    // on this repository
	Overall Stat    // on every repository but the toy fixtures
	Rate    float64 // estimated pass rate on this repository
	PerHour float64 // estimated passes per hour of agent time; 0 if never timed
	Score   float64
	Down    string // why it is skipped, if it is
}

// Pick orders backends for a task on repo: backends that are up first, by
// score (ties keep the config order); down ones last, marked.
//
// The pass-rate estimate shrinks the backend's record on this repository
// toward its record everywhere else (weighted as priorWeight runs); a backend
// with no real record at all starts at 1/2. So one lucky run cannot win, and a
// repository nobody has tried is judged by each backend's general record.
func Pick(cfg *Config, st Stats, repo string, down map[string]string) []Ranked {
	var out []Ranked
	for _, b := range cfg.Backends {
		name := b.StatsModel
		if name == "" {
			name = b.Model
		}
		var all Stat
		for r, s := range st[name] {
			if r != "toy" {
				all.Scored += s.Scored
				all.Pass += s.Pass
				all.WallMS += s.WallMS
			}
		}
		s := st[name][repo]
		prior := float64(all.Pass+1) / float64(all.Scored+2)
		rate := (float64(s.Pass) + priorWeight*prior) / (float64(s.Scored) + priorWeight)
		timed := s
		if timed.Scored == 0 || timed.WallMS == 0 {
			timed = all
		}
		perHour := 0.0
		if timed.Scored > 0 && timed.WallMS > 0 {
			perHour = rate / (float64(timed.WallMS) / float64(timed.Scored) / 3.6e6)
		}
		score := rate
		if cfg.Objective == "pass_per_hour" {
			score = perHour
		}
		out = append(out, Ranked{Backend: b, Stat: s, Overall: all, Rate: rate, PerHour: perHour, Score: score, Down: down[b.Name]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Down == "") != (out[j].Down == "") {
			return out[i].Down == ""
		}
		return out[i].Score > out[j].Score
	})
	return out
}

// Order is Pick's backends that are up, in order.
func Order(r []Ranked) []Backend {
	var out []Backend
	for _, x := range r {
		if x.Down == "" {
			out = append(out, x.Backend)
		}
	}
	return out
}
