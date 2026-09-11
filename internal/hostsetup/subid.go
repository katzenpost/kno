package hostsetup

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const subidCount = 65536

const subidStart = 100000

func NextSubidRange(acct string) string {
	have := true
	max := subidStart
	for _, f := range []string{"/etc/subuid", "/etc/subgid"} {
		b, err := os.ReadFile(f)
		if err != nil {

			have = false
			continue
		}
		mine, top := ScanSubids(string(b), acct)
		if !mine {
			have = false
		}
		if top > max {
			max = top
		}
	}
	if have {
		return ""
	}
	return FormatRange(max)
}

func FormatRange(start int) string {
	return fmt.Sprintf("%d-%d", start, start+subidCount-1)
}

func ScanSubids(body, acct string) (mine bool, next int) {
	next = subidStart
	for _, line := range strings.Split(body, "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) != 3 {
			continue
		}
		if f[0] == acct {
			mine = true
		}
		start, err1 := strconv.Atoi(f[1])
		count, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil {
			continue
		}
		if end := start + count; end > next {
			next = end
		}
	}
	return mine, next
}
