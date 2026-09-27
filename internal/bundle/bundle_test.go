package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRun(t *testing.T, verdict string, claimed bool) string {
	t.Helper()
	d := t.TempDir()
	claim := "false"
	if claimed {
		claim = "true"
	}
	os.WriteFile(filepath.Join(d, "summary.json"), []byte(`{"task":"t-1","model":"m","harness":"0.6.1","verdict":"`+verdict+`","reasons":["r"],"agent_claimed_fixed":`+claim+`,"stop_reason":"agent submitted"}`), 0o644)
	os.WriteFile(filepath.Join(d, "final.patch"), []byte("--- a/x\n+++ b/x\n"), 0o644)
	os.MkdirAll(filepath.Join(d, "final"), 0o755)
	os.WriteFile(filepath.Join(d, "final", "evidence.json"), []byte(`{"verdict":"`+verdict+`","task":{"issue":"Thing is broken\nmore","required_tests":["a","b"]},"tests":{"missing_required":[]},"sandbox":{"network":"none"}}`), 0o644)
	os.WriteFile(filepath.Join(d, "transcript.jsonl"), []byte(`{"role":"assistant","tool_calls":[{"function":{"name":"submit","arguments":"{\"fixed\":true,\"summary\":\"did it\"}"}}]}`+"\n"), 0o644)
	return d
}

func TestReadyBundle(t *testing.T) {
	out := t.TempDir()
	res, err := Write(writeRun(t, "PASS", true), out)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(res.BodyPath)
	for _, want := range []string{"READY FOR REVIEW", "Thing is broken", "did it", "and it matches the claim", "loopback only", "Nothing was pushed"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("pr.md lacks %q", want)
		}
	}
	if !res.Ready {
		t.Error("PASS run not ready")
	}
}

// An over-claim is called out, and the bundle is not ready.
func TestOverClaimIsNotReady(t *testing.T) {
	res, err := Write(writeRun(t, "FAIL", true), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(res.BodyPath)
	if res.Ready || !strings.Contains(string(body), "NOT READY") || !strings.Contains(string(body), "does not match") {
		t.Errorf("ready=%v body:\n%s", res.Ready, body)
	}
}
