package cockroach

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// sinkless runs each changefeed as a query on a dedicated connection, which
// streams the messages back as rows. The balancer picks the node the
// connection reaches, and with it the node that runs the changefeed.
type sinkless struct {
	balancer *feedBalancer
}

func (s sinkless) open(ctx context.Context, cursor hlc) (changefeedStream, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	// A changefeed holds its connection for as long as it runs.
	conn, err := s.balancer.dial(ctx, func() { cancel(errFeedRebalanced) })
	if err != nil {
		cancel(nil)
		return nil, err
	}
	rows, err := conn.Query(ctx, `CREATE CHANGEFEED FOR TABLE objects WITH `+strings.Join(feedOptions, ", ")+`, cursor = '`+cursor.String()+`'`)
	if err != nil {
		cancel(nil)
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	return &sinklessStream{ctx: ctx, cancel: cancel, conn: conn, rows: rows}, nil
}

// refused reports a database error other than a cancelled query.
func (sinkless) refused(err error) bool {
	pe, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pe.Code != codeQueryCanceled
}

// earliest is zero: a sinkless changefeed starts from any cursor within the
// GC window.
func (sinkless) earliest(context.Context) (hlc, error) { return hlc{}, nil }

type sinklessStream struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	conn   *pgx.Conn
	rows   pgx.Rows
	failed error
}

func (s *sinklessStream) next() (feedMessage, bool) {
	if s.failed != nil || !s.rows.Next() {
		return feedMessage{}, false
	}
	values := s.rows.RawValues()
	if len(values) != 3 {
		s.failed = fmt.Errorf("%w: a row has %d columns, want 3", errBadMessage, len(values))
		return feedMessage{}, false
	}
	msg, err := decodeFeedMessage(values[2])
	if err != nil {
		s.failed = err
		return feedMessage{}, false
	}
	return msg, true
}

func (s *sinklessStream) err() error {
	if s.failed != nil {
		return s.failed
	}
	if cause := context.Cause(s.ctx); errors.Is(cause, errFeedRebalanced) {
		return cause
	}
	if err := s.rows.Err(); err != nil {
		return err
	}
	return errors.New("the changefeed ended")
}

func (s *sinklessStream) close() {
	// Closing rows reads until the query ends, and a changefeed never does,
	// so the query is cancelled first.
	s.cancel(nil)
	s.rows.Close()
	_ = s.conn.Close(context.WithoutCancel(s.ctx))
}
