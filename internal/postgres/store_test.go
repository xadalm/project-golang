package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "serialization failure", err: &pgconn.PgError{Code: "40001"}, want: ErrTransactionRetryable},
		{name: "deadlock", err: &pgconn.PgError{Code: "40P01"}, want: ErrTransactionRetryable},
		{name: "lock timeout", err: &pgconn.PgError{Code: "55P03"}, want: ErrTransactionRetryable},
		{name: "statement timeout", err: &pgconn.PgError{Code: "57014"}, want: ErrDatabaseTimeout},
		{name: "constraint failure", err: &pgconn.PgError{Code: "23503"}, want: ErrDatabaseFailure},
		{name: "context deadline", err: context.DeadlineExceeded, want: ErrDatabaseTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := classifyError("test operation", test.err); !errors.Is(err, test.want) {
				t.Fatalf("classifyError() = %v, want errors.Is(_, %v)", err, test.want)
			}
		})
	}
}
