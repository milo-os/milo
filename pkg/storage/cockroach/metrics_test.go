package cockroach

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestErrorCode(t *testing.T) {
	require.Equal(t, codeSerializationFailure, errorCode(fmt.Errorf("commit: %w", &pgconn.PgError{Code: codeSerializationFailure})))
	require.Equal(t, "canceled", errorCode(context.DeadlineExceeded))
	require.Equal(t, "other", errorCode(errors.New("connection reset")))
}
