package compile

import (
	"slices"
	"testing"
)

func TestContainedNamesARunAndForgetsItWhenDone(t *testing.T) {
	args, done := Contained([]string{"run", "--rm", "img", "true"})
	if len(args) != 6 || args[1] != "--name" || args[3] != "--rm" {
		t.Fatalf("args = %v", args)
	}
	if !slices.Contains(Running(), args[2]) {
		t.Errorf("%s is not listed as running", args[2])
	}
	done()
	if slices.Contains(Running(), args[2]) {
		t.Errorf("%s is still listed after done", args[2])
	}
	if got, _ := Contained([]string{"build", "-q"}); len(got) != 2 {
		t.Errorf("a build got a name: %v", got)
	}
}
