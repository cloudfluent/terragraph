package engine

import (
	"context"
	"errors"
	"reflect"

	"github.com/cloudfluent/terragraph/internal/graphlock"
	"github.com/cloudfluent/terragraph/internal/runlock"
)

// diagnosticError preserves error identity while exposing decisions without parsing human messages.
type diagnosticError struct {
	cause      error
	diagnostic Diagnostic
}

func (e *diagnosticError) Error() string { return e.cause.Error() }
func (e *diagnosticError) Unwrap() error { return e.cause }

// WithDiagnostic attaches branching metadata without changing the underlying error chain.
func WithDiagnostic(err error, diagnostic Diagnostic) error {
	if err == nil {
		return nil
	}
	if diagnostic.Message == "" {
		diagnostic.Message = err.Error()
	}
	return &diagnosticError{cause: err, diagnostic: diagnostic}
}

// Diagnostics walks joined causes independently and skips already reported error identities so node reports cannot hide global journal failures.
func Diagnostics(err error, fallback Diagnostic, excluded ...error) []Diagnostic {
	result := []Diagnostic{}
	seen := map[Diagnostic]bool{}
	add := func(d Diagnostic) {
		if d.Severity == "" {
			d.Severity = "error"
		}
		if d.Category == "" {
			d.Category = "unknown"
		}
		if !seen[d] {
			result = append(result, d)
			seen[d] = true
		}
	}
	var walk func(error, Diagnostic)
	walk = func(err error, current Diagnostic) {
		if err == nil {
			return
		}
		if reflect.TypeOf(err).Comparable() {
			for _, skip := range excluded {
				if err == skip {
					return
				}
			}
		}
		if typed, ok := err.(*diagnosticError); ok {
			diagnostic := typed.diagnostic
			if diagnostic.Subject == "" {
				diagnostic.Subject = current.Subject
			}
			if diagnostic.Phase == "" {
				diagnostic.Phase = current.Phase
			}
			if diagnostic.Category == "" {
				diagnostic.Category = current.Category
			}
			walk(typed.cause, diagnostic)
			return
		}
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				diagnostic := current
				diagnostic.Message = ""
				walk(child, diagnostic)
			}
			return
		}
		if wrapped, ok := err.(interface{ Unwrap() error }); ok {
			if current.Message == "" {
				current.Message = err.Error()
			}
			walk(wrapped.Unwrap(), current)
			return
		}
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			if current.Category != "record" {
				current.Code, current.Category = "cancelled", "cancelled"
			}
		case errors.Is(err, runlock.ErrHeld), errors.Is(err, graphlock.ErrHeld):
			current.Code, current.Category = "lock_held", "lock"
			current.Remedy = "wait for the current executor to finish before retrying"
		case errors.Is(err, errExecutionConflict):
			current.Code, current.Category = "execution_record_conflict", "record"
		}
		if current.Code == "" {
			current.Code = "runtime_failed"
		}
		if current.Message == "" {
			current.Message = err.Error()
		}
		add(current)
	}
	walk(err, fallback)
	return result
}
