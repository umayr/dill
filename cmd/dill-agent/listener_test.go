package main

import (
	"net"
	"testing"
	"time"
)

func TestConnectionLimitRecovers(t *testing.T) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	limited := newLimitedListener(l, 1)
	defer limited.Close()
	accepted := make(chan net.Conn, 2)
	go func() {
		for {
			c, e := limited.Accept()
			if e != nil {
				return
			}
			accepted <- c
		}
	}()
	one, e := net.Dial("tcp", l.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer one.Close()
	serverOne := <-accepted
	two, e := net.Dial("tcp", l.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer two.Close()
	_ = two.SetReadDeadline(time.Now().Add(2 * time.Second))
	var b [1]byte
	if _, e = two.Read(b[:]); e == nil {
		t.Fatal("excess connection remained open")
	}
	serverOne.Close()
	three, e := net.Dial("tcp", l.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer three.Close()
	select {
	case c := <-accepted:
		c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("capacity did not recover")
	}
}
