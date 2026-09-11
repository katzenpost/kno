package doctor

import "testing"

func TestAnAlternativeIsHiddenWhenItsPreferredToolIsPresent(t *testing.T) {
	podman := Tool{Name: "podman", Scope: ScopeNode}
	docker := Tool{Name: "docker", Scope: ScopeNode, AltFor: "podman"}

	got := dropSatisfiedAlternatives([]Status{
		{Tool: podman, Name: "podman", Found: true},
		{Tool: docker, Name: "docker", Found: false},
	})
	if len(got) != 1 || got[0].Name != "podman" {
		t.Errorf("docker was listed while podman is present: %+v", names(got))
	}

	got = dropSatisfiedAlternatives([]Status{
		{Tool: podman, Name: "podman", Found: false},
		{Tool: docker, Name: "docker", Found: false},
	})
	if len(got) != 2 {
		t.Errorf("the alternative was hidden with nothing to satisfy it: %+v", names(got))
	}

	got = dropSatisfiedAlternatives([]Status{
		{Tool: podman, Name: "podman", Found: false},
		{Tool: docker, Name: "docker", Found: true},
	})
	if len(got) != 2 {
		t.Errorf("the engine that IS present was hidden: %+v", names(got))
	}
}

func names(ss []Status) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Name)
	}
	return out
}
