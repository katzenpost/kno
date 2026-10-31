package doctor

import "testing"

func TestDoctorRequiresOnlyTheSelectedRootHelper(t *testing.T) {
	t.Setenv("SUDO", "su")
	for _, tool := range Tools {
		switch tool.Name {
		case "su":
			if !tool.wanted() {
				t.Fatal("selected su helper is not checked")
			}
		case "sudo", "doas":
			if tool.wanted() {
				t.Fatalf("su host unnecessarily requires %s", tool.Name)
			}
		}
	}
}
