package verify

// FileDiff is one file's part of a unified diff.
type FileDiff struct {
	Path string
	Text string
}

// SplitPatch cuts a unified diff (diff -ruN or git style) into per-file parts.
func SplitPatch(p string) []FileDiff {
	var out []FileDiff
	for _, fd := range splitPatch(p) {
		out = append(out, FileDiff{Path: fd.path, Text: fd.text})
	}
	return out
}
