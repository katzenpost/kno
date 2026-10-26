package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
)

type PrefixWriter struct {
	w      io.Writer
	prefix string

	mu  sync.Mutex
	buf bytes.Buffer

	lastCR bool
}

func NewPrefixWriter(w io.Writer, prefix string) io.Writer {
	if prefix == "" {
		return w
	}
	return &PrefixWriter{w: w, prefix: prefix}
}

const maxLine = 64 << 10

func (p *PrefixWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf.Write(b)
	for {
		buffered := p.buf.Bytes()
		i := bytes.IndexAny(buffered, "\n\r")
		if i < 0 && len(buffered) > maxLine {
			line := Clean(string(buffered[:maxLine]))
			p.buf.Next(maxLine)
			if _, err := io.WriteString(p.w, p.prefix+line+"\n"); err != nil {
				return len(b), err
			}
			continue
		}
		if i < 0 {
			break
		}
		end := i + 1

		if buffered[i] == '\r' && end < len(buffered) && buffered[end] == '\n' {
			end++
		}
		line := string(buffered[:end])
		p.buf.Next(end)

		if line == "\n" && p.lastCR {
			p.lastCR = false
			continue
		}
		p.lastCR = strings.HasSuffix(line, "\r")
		end2 := strings.TrimRight(line, "\r\n")
		if _, err := io.WriteString(p.w, p.prefix+Clean(end2)+line[len(end2):]); err != nil {
			return len(b), err
		}
	}
	return len(b), nil
}

func (p *PrefixWriter) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.buf.Len() == 0 {
		return nil
	}
	_, err := io.WriteString(p.w, p.prefix+Clean(p.buf.String())+"\n")
	p.buf.Reset()
	return err
}

func HostPrefix(label string) string {
	if label == "" {
		return ""
	}
	return "  " + label + "| "
}

func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
