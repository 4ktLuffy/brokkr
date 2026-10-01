package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/4ktLuffy/brokkr/internal/replay"
)

// replayCmd writes one self-contained HTML page for a finished run (or two
// side by side with --compare).
func replayCmd(argv []string) {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	run := fs.String("run", "", "a `brokkr fix` output directory")
	cmp := fs.String("compare", "", "a second run of the same task, shown side by side")
	out := fs.String("out", "", "HTML file to write")
	maxResult := fs.Int("max-result", 0, "characters kept per tool result (default 20000)")
	_ = fs.Parse(argv)
	if *run == "" || *out == "" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err := replay.WriteFile(*run, *cmp, *out, replay.Options{MaxResult: *maxResult}); err != nil {
		die(err)
	}
	st, _ := os.Stat(*out)
	fmt.Printf("wrote %s (%d bytes)\n", *out, st.Size())
}
