package store

import (
	"context"
	"database/sql/driver"
	"sync/atomic"
)

// sqlQueryObserver is a test seam: when set before Open, connections report
// every SQL statement executed through them. Production never sets it, so the
// normal driver path stays untouched.
var sqlQueryObserver atomic.Pointer[func(string)]

// SetSQLQueryObserverForTest installs an observer for Stores opened after this
// call. Tests use it to prove a read path issues a bounded number of queries.
// Pass nil to clear it.
func SetSQLQueryObserverForTest(fn func(string)) {
	if fn == nil {
		sqlQueryObserver.Store(nil)
		return
	}
	observer := fn
	sqlQueryObserver.Store(&observer)
}

type observedConnector struct {
	driver.Connector
	observe func(string)
}

func (c observedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &observedConn{Conn: conn, observe: c.observe}, nil
}

// observedConn implements only the context exec/query interfaces. When the
// underlying driver lacks one, database/sql falls back to prepare on the
// promoted driver.Conn methods.
type observedConn struct {
	driver.Conn
	observe func(string)
}

func (c *observedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.observe(query)
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return queryer.QueryContext(ctx, query, args)
}

func (c *observedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.observe(query)
	execer, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return execer.ExecContext(ctx, query, args)
}
