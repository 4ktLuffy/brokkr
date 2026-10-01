// Package codemap gives the agent code navigation and a static check for
// Python repositories: find_definition, find_usages, outline, and a
// high-confidence check of an edited file.
//
// Everything is done by two stdlib-only Python scripts (index.py, check.py)
// run with the host's python3. They only parse files with ast; nothing from the
// repository is imported or executed, the same rule as the agent's syntax check.
package codemap

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed index.py check.py
var scripts embed.FS

const (
	// queryTimeout bounds one index request. The first one builds the index
	// (a few seconds for Django); later ones re-parse only changed files.
	queryTimeout = 120 * time.Second
	checkTimeout = 10 * time.Second
)

// Index answers questions about the Python files under one directory. The
// Python process starts on the first query, keeps the index in memory, and
// notices edited files by itself (size and mtime), so there is no refresh call.
type Index struct {
	root string

	mu     sync.Mutex
	dir    string // holds the scripts
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	failed string // why python is unusable, if it is
	closed bool
}

func New(root string) *Index { return &Index{root: root} }

// Warm builds the index in the background, so the first question the model
// asks does not wait for it. Queries made meanwhile wait their turn.
func (ix *Index) Warm() { go ix.Query("stats", "") }

// python returns the interpreter and writes the scripts out, once.
func (ix *Index) python() (string, error) {
	if ix.closed {
		return "", fmt.Errorf("code index is closed")
	}
	if ix.failed != "" {
		return "", fmt.Errorf("%s", ix.failed)
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		ix.failed = "code tools are unavailable: no python3 on the host. Use search and read_file."
		return "", fmt.Errorf("%s", ix.failed)
	}
	if ix.dir == "" {
		d, err := os.MkdirTemp("", "brokkr-codemap-")
		if err != nil {
			return "", err
		}
		for _, f := range []string{"index.py", "check.py"} {
			b, _ := scripts.ReadFile(f)
			if err := os.WriteFile(filepath.Join(d, f), b, 0o644); err != nil {
				return "", err
			}
		}
		ix.dir = d
	}
	return py, nil
}

func (ix *Index) start() error {
	py, err := ix.python()
	if err != nil {
		return err
	}
	cmd := exec.Command(py, "-I", filepath.Join(ix.dir, "index.py"), ix.root)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	ix.cmd, ix.in, ix.out = cmd, in, bufio.NewReaderSize(out, 1<<16)
	return nil
}

func (ix *Index) stop() {
	if ix.cmd != nil {
		_ = ix.in.Close()
		_ = ix.cmd.Process.Kill()
		_, _ = ix.cmd.Process.Wait()
		ix.cmd, ix.in, ix.out = nil, nil, nil
	}
}

// Query runs one request (op is find_definition, find_usages, outline or
// stats) and returns the text for the model. A failure comes back as an
// "error: ..." string, never a Go error, because the model is the reader.
func (ix *Index) Query(op, arg string) string {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.cmd == nil {
		if err := ix.start(); err != nil {
			return "error: " + err.Error()
		}
	}
	req, _ := json.Marshal(map[string]string{"op": op, "arg": arg})
	type reply struct {
		text string
		err  error
	}
	ch := make(chan reply, 1)
	go func() {
		if _, err := ix.in.Write(append(req, '\n')); err != nil {
			ch <- reply{err: err}
			return
		}
		line, err := ix.out.ReadBytes('\n')
		if err != nil {
			ch <- reply{err: err}
			return
		}
		var r struct{ Text string }
		if err := json.Unmarshal(line, &r); err != nil {
			ch <- reply{err: err}
			return
		}
		ch <- reply{text: r.Text}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	select {
	case r := <-ch:
		if r.err != nil {
			ix.stop() // restart on the next query
			return "error: code index failed: " + r.err.Error()
		}
		return r.text
	case <-ctx.Done():
		ix.stop()
		return "error: code index timed out; use search and read_file"
	}
}

// Check runs the static check on file rel (relative to the index root) and
// returns the problems it introduces compared with the same file under
// origRoot, one per line, or "" when there are none or the check cannot run.
func (ix *Index) Check(rel, origRoot string) string {
	ix.mu.Lock()
	py, err := ix.python()
	dir := ix.dir
	ix.mu.Unlock()
	if err != nil || !strings.HasSuffix(rel, ".py") {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	out, _ := exec.CommandContext(ctx, py, "-I", filepath.Join(dir, "check.py"),
		filepath.Join(ix.root, rel), filepath.Join(origRoot, rel)).Output()
	return strings.TrimSpace(string(out))
}

func (ix *Index) Close() {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.closed = true
	ix.stop()
	if ix.dir != "" {
		_ = os.RemoveAll(ix.dir)
		ix.dir = ""
	}
}
