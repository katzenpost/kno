package freebsd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestPietjeUpgradeCleansStoppedJailMountsFromChildrenToRoot(t *testing.T) {
	for _, live := range []bool{false, true} {
		t.Run(fmt.Sprint("live=", live), func(t *testing.T) {
			root := Jail{Name: "kpauthority"}.Path()
			mounts := []string{root, root + "/usr/local", root + "/dev", root + "/var/lib/pq-katzenpost-authority"}
			var removed []string
			c := jailControl{run: func(_ context.Context, args ...string) ([]byte, error) {
				switch args[0] {
				case "jls":
					if live {
						return []byte("name=kpauthority\nname=kpserver\n"), nil
					}
					return []byte("name=kpserver\n"), nil
				case "service":
					if strings.Join(args, " ") != "service jail stop kpauthority" {
						t.Fatalf("unexpected stop: %v", args)
					}
					live = false
					return nil, nil
				case "mount":
					var out strings.Builder
					fmt.Fprintln(&out, "/unrelated /var/jail/kpserver nullfs ro 0 0")
					fmt.Fprintln(&out, "/unrelated /var/jail/kpauthority-other nullfs ro 0 0")
					for _, target := range mounts {
						fmt.Fprintf(&out, "/source %s nullfs ro 0 0\n", target)
					}
					return []byte(out.String()), nil
				case "umount":
					if len(args) != 2 || !slices.Contains(mounts, args[1]) {
						t.Fatalf("forced or unrelated unmount: %v", args)
					}
					for _, target := range mounts {
						if strings.HasPrefix(target, args[1]+"/") {
							return nil, errors.New("Device busy")
						}
					}
					removed = append(removed, args[1])
					mounts = slices.DeleteFunc(mounts, func(s string) bool { return s == args[1] })
					return nil, nil
				}
				t.Fatalf("unexpected command: %v", args)
				return nil, nil
			}}
			if err := c.stop(t.Context(), "kpauthority", io.Discard); err != nil {
				t.Fatal(err)
			}
			if len(removed) != 4 || removed[3] != root {
				t.Fatalf("incomplete or unsafe cleanup: %v", removed)
			}
		})
	}
}

func TestJailCleanupRefusesBusyMountsAndUnknownMountListings(t *testing.T) {
	for _, listing := range []string{
		"/source /var/jail/kpauthority nullfs ro 0 0\n",
		"unrecognised mount output\n",
	} {
		var calls []string
		c := jailControl{run: func(_ context.Context, args ...string) ([]byte, error) {
			calls = append(calls, strings.Join(args, " "))
			switch args[0] {
			case "jls":
				return nil, nil
			case "mount":
				return []byte(listing), nil
			case "umount":
				return nil, errors.New("Device busy")
			}
			t.Fatalf("unexpected recovery command: %v", args)
			return nil, nil
		}}
		if err := c.stop(t.Context(), "kpauthority", io.Discard); err == nil {
			t.Fatalf("unsafe cleanup succeeded: %v", calls)
		}
		for _, call := range calls {
			if strings.Contains(call, "-f") || strings.Contains(call, "rm ") {
				t.Fatalf("destructive recovery: %s", call)
			}
		}
	}
}

func TestDyingJailPreventsAnyUnmount(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	c := jailControl{run: func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "jls" {
			return []byte("name=kpauthority\n"), nil
		}
		if args[0] == "service" {
			cancel()
			return nil, errors.New("jail is dying")
		}
		t.Fatalf("command ran while jail remained dying: %v", args)
		return nil, nil
	}}
	if err := c.stop(ctx, "kpauthority", io.Discard); err == nil {
		t.Fatal("a dying jail was treated as stopped")
	}
}
