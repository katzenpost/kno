package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

type ExitError struct{ Code int }

func (e *ExitError) Error() string { return "" }
func (e *ExitError) ExitCode() int { return e.Code }

func Exit(code int) error {
	if code == 0 {
		return nil
	}
	return &ExitError{Code: code}
}

func Failf(w io.Writer, code int, format string, a ...interface{}) error {
	fmt.Fprintf(w, format+"\n", a...)
	return &ExitError{Code: code}
}

type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }
func (e *UsageError) ExitCode() int { return 2 }

func Args(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := v(cmd, args); err != nil {
			return &UsageError{Err: err}
		}
		return nil
	}
}

func FlagError(_ *cobra.Command, err error) error { return &UsageError{Err: err} }
