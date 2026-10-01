package mutate

import "github.com/4ktLuffy/brokkr/internal/verify"

func splitOne(diff string) verify.FileDiff { return verify.SplitPatch(diff)[0] }

func splitFix(patch string) []verify.FileDiff {
	_, rest, _ := verify.SplitNewTests(patch, "tests/", "/nonexistent")
	return verify.SplitPatch(rest)
}
