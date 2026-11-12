package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type FixtureConfig struct {
	HeaderDelay, BodyDelay, SlowDelay, TLSDelay time.Duration
	SlowEvery                                   int
	SlowStage                                   string
	TLS                                         bool
}

func waitDelay(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
func fixtureHandler(cfg FixtureConfig) http.Handler {
	var count atomic.Int64
	body := strings.Repeat("x", 64)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.Header.Get("X-Lab-Request-ID"))
		if err != nil {
			id = int(count.Add(1))
		}
		slow := cfg.SlowEvery > 0 && id > 0 && id%cfg.SlowEvery == 0
		hd, bd := cfg.HeaderDelay, cfg.BodyDelay
		if slow {
			if cfg.SlowStage == "body" {
				bd += cfg.SlowDelay
			} else {
				hd += cfg.SlowDelay
			}
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("X-Lab-Slow", strconv.FormatBool(slow))
		if !waitDelay(r.Context(), hd) {
			return
		}
		if bd > 0 {
			w.Write([]byte(body[:1]))
			w.(http.Flusher).Flush()
			if !waitDelay(r.Context(), bd) {
				return
			}
			w.Write([]byte(body[1:]))
			return
		}
		w.Write([]byte(body))
	})
}
func startFixture(cfg FixtureConfig) *httptest.Server {
	srv := httptest.NewUnstartedServer(fixtureHandler(cfg))
	srv.Config.ReadHeaderTimeout = 5 * time.Second
	if cfg.TLS {
		srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}, GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
			if !waitDelay(info.Context(), cfg.TLSDelay) {
				return nil, info.Context().Err()
			}
			return nil, nil
		}}
		srv.StartTLS()
	} else {
		srv.Start()
	}
	return srv
}

// This small authoritative fixture answers A/AAAA for fixture.test only. Its
// synthetic delay happens before real DNS replies, so httptrace observes an
// actual net.Resolver exchange. It is not a production DNS implementation.
type dnsFixture struct {
	conn       net.PacketConn
	delay      time.Duration
	stop, done chan struct{}
	workers    sync.WaitGroup
	once       sync.Once
}

func startDNSFixture(delay time.Duration) (*dnsFixture, error) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	d := &dnsFixture{conn: conn, delay: delay, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(d.done)
		buffer := make([]byte, 4096)
		for {
			n, addr, err := conn.ReadFrom(buffer)
			if err != nil {
				return
			}
			packet := append([]byte(nil), buffer[:n]...)
			d.workers.Add(1)
			go func() {
				defer d.workers.Done()
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-d.stop:
					return
				}
				if reply := dnsReply(packet); reply != nil {
					conn.WriteTo(reply, addr)
				}
			}()
		}
	}()
	return d, nil
}
func dnsReply(query []byte) []byte {
	if len(query) < 12 || binary.BigEndian.Uint16(query[4:6]) != 1 {
		return nil
	}
	pos := 12
	labels := []string{}
	for {
		if pos >= len(query) {
			return nil
		}
		n := int(query[pos])
		pos++
		if n == 0 {
			break
		}
		if n > 63 || pos+n > len(query) {
			return nil
		}
		labels = append(labels, string(query[pos:pos+n]))
		pos += n
	}
	if pos+4 > len(query) {
		return nil
	}
	typ := binary.BigEndian.Uint16(query[pos : pos+2])
	class := binary.BigEndian.Uint16(query[pos+2 : pos+4])
	end := pos + 4
	reply := make([]byte, 12, end+16)
	copy(reply[:2], query[:2])
	binary.BigEndian.PutUint16(reply[2:4], 0x8580)
	binary.BigEndian.PutUint16(reply[4:6], 1)
	reply = append(reply, query[12:end]...)
	if strings.ToLower(strings.Join(labels, ".")) != "fixture.test" || class != 1 {
		binary.BigEndian.PutUint16(reply[2:4], 0x8583)
		return reply
	}
	if typ == 1 {
		binary.BigEndian.PutUint16(reply[6:8], 1)
		reply = append(reply, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4, 127, 0, 0, 1)
	}
	return reply
}
func resolverAt(address string) *net.Resolver {
	return &net.Resolver{PreferGo: true, StrictErrors: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
}
func (d *dnsFixture) Close() {
	d.once.Do(func() { close(d.stop); d.conn.Close(); <-d.done; d.workers.Wait() })
}
func (d *dnsFixture) Resolver() *net.Resolver { return resolverAt(d.conn.LocalAddr().String()) }
