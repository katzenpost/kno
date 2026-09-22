package sim

import "testing"

func TestTheRegisterLineCopiesTheHostEntry(t *testing.T) {
	entry := "enabled\ninterpreter /usr/bin/qemu-ppc64le\nflags: POF\noffset 0\n" +
		"magic 7f454c4602010100000000000000000002001500\n" +
		"mask fffffffffffffffcfffffffffffffffffeffff00\n"
	got, err := registerLine("qemu-ppc64le", entry)
	if err != nil {
		t.Fatal(err)
	}
	want := `:qemu-ppc64le:M::\x7f\x45\x4c\x46\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x15\x00:` +
		`\xff\xff\xff\xff\xff\xff\xff\xfc\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\x00:/usr/bin/qemu-ppc64le:POF`
	if got != want {
		t.Errorf("register line\n got %s\nwant %s", got, want)
	}
	if _, err := registerLine("qemu-x", "enabled\nflags: F\n"); err == nil {
		t.Error("an entry with no magic gave a register line")
	}
}

func TestQemuNamesFollowGoArch(t *testing.T) {
	for goarch, want := range map[string]string{"ppc64le": "qemu-ppc64le", "arm64": "qemu-aarch64"} {
		if got := qemuName(goarch); got != want {
			t.Errorf("qemuName(%s) = %s, want %s", goarch, got, want)
		}
	}
}
