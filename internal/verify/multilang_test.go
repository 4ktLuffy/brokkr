package verify

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/multilang/cases.json holds sample logs and the statuses SWE-bench's
// own Python parsers (02e7a74ffd0b) produce for them; the ports must agree on
// every name. Regenerate with the script recorded in NIGHTLOG.
func TestMultilangParsersMatchSWEbench(t *testing.T) {
	raw, err := os.ReadFile("testdata/multilang/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases map[string]struct {
		Log      string            `json:"log"`
		Expected map[string]string `json:"expected"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != len(multilangParsers) {
		t.Fatalf("%d cases for %d parsers", len(cases), len(multilangParsers))
	}
	for name, c := range cases {
		passed, failed, ok := parseMultilang(name, c.Log)
		if !ok {
			t.Fatalf("%s: unknown parser", name)
		}
		want := map[string]bool{}
		for test, st := range c.Expected {
			want[test] = true
			switch st {
			case "PASSED":
				if !passed[test] || failed[test] {
					t.Errorf("%s: %q should be passed", name, test)
				}
			case "FAILED":
				if !failed[test] || passed[test] {
					t.Errorf("%s: %q should be failed", name, test)
				}
			default: // SKIPPED: neither
				if passed[test] || failed[test] {
					t.Errorf("%s: skipped %q counted", name, test)
				}
			}
		}
		for test := range passed {
			if !want[test] {
				t.Errorf("%s: extra passed %q", name, test)
			}
		}
		for test := range failed {
			if !want[test] {
				t.Errorf("%s: extra failed %q", name, test)
			}
		}
	}
	if _, _, ok := parseMultilang("nope", ""); ok {
		t.Error("unknown parser accepted")
	}
}
