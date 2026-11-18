package agentcmd

import (
	"net"
	"sync"
)

// limitedListener bounds simultaneously accepted HTTP connections. Excess
// connections are closed immediately, without an unbounded waiting queue.
type limitedListener struct {
	net.Listener
	slots chan struct{}
}

func newLimitedListener(l net.Listener, n int) *limitedListener {
	return &limitedListener{Listener: l, slots: make(chan struct{}, n)}
}
func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		c, e := l.Listener.Accept()
		if e != nil {
			return nil, e
		}
		select {
		case l.slots <- struct{}{}:
			return &limitedConn{Conn: c, release: func() { <-l.slots }}, nil
		default:
			_ = c.Close()
		}
	}
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error { e := c.Conn.Close(); c.once.Do(c.release); return e }
