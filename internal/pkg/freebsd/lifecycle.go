package freebsd

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var jailName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

type jailControl struct {
	run func(context.Context, ...string) ([]byte, error)
}

func StopJail(ctx context.Context, name string, out io.Writer) error {
	c := jailControl{run: func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		if args[0] == "mount" {
			return cmd.Output()
		}
		return cmd.CombinedOutput()
	}}
	return c.stop(ctx, name, out)
}

func (c jailControl) exists(ctx context.Context, name string) (bool, error) {
	out, err := c.run(ctx, "jls", "-d", "-n", "name")
	if err != nil {
		return false, fmt.Errorf("list live and dying jails: %w: %s", err, out)
	}
	for _, field := range strings.Fields(string(out)) {
		value, ok := strings.CutPrefix(field, "name=")
		if !ok {
			return false, fmt.Errorf("unexpected jail listing: %s", out)
		}
		if strings.Trim(value, `"`) == name {
			return true, nil
		}
	}
	return false, nil
}

func mountTargets(out []byte, root string) ([]string, error) {
	var targets []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 6 {
			return nil, fmt.Errorf("unexpected mount listing: %s", line)
		}
		target := fields[1]
		if target == root || strings.HasPrefix(target, root+"/") {
			targets = append(targets, target)
		}
	}
	sort.SliceStable(targets, func(i, j int) bool {
		if a, b := strings.Count(targets[i], "/"), strings.Count(targets[j], "/"); a != b {
			return a > b
		}
		return targets[i] > targets[j]
	})
	return targets, nil
}

func (c jailControl) stop(ctx context.Context, name string, out io.Writer) error {
	if !jailName.MatchString(name) {
		return fmt.Errorf("invalid jail name %q", name)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	exists, err := c.exists(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		result, stopErr := c.run(ctx, "service", "jail", "stop", name)
		fmt.Fprint(out, string(result))
		for {
			exists, err = c.exists(ctx, name)
			if err != nil {
				return err
			}
			if !exists {
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("jail %s is still live or dying after stop (%v): %w", name, stopErr, ctx.Err())
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	root := filepath.Join(JailsDir, name)
	mounts, err := c.run(ctx, "mount", "-p")
	if err != nil {
		return fmt.Errorf("list mounts before changing %s: %w: %s", name, err, mounts)
	}
	targets, err := mountTargets(mounts, root)
	if err != nil {
		return err
	}
	for _, target := range targets {
		fmt.Fprintf(out, "unmount stale jail mount %s\n", target)
		result, err := c.run(ctx, "umount", target)
		if err != nil {
			return fmt.Errorf("unmount %s; jail configuration and state have not been changed: %w: %s", target, err, result)
		}
	}
	mounts, err = c.run(ctx, "mount", "-p")
	if err != nil {
		return fmt.Errorf("check mounts after stopping %s: %w", name, err)
	}
	targets, err = mountTargets(mounts, root)
	if err != nil {
		return err
	}
	if len(targets) != 0 {
		return fmt.Errorf("jail %s still has mounted filesystems: %v", name, targets)
	}
	return nil
}
