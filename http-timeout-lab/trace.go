package main

import (
	"context"
	"crypto/tls"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"
)

type recorder struct {
	mu     sync.Mutex
	start  time.Time
	events []Event
}

func newRecorder(start time.Time) *recorder {
	return &recorder{start: start, events: make([]Event, 0, 20)}
}
func withRecorder(ctx context.Context, r *recorder) context.Context {
	return httptrace.WithClientTrace(ctx, r.trace())
}
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
func (r *recorder) add(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e.At = time.Now()
	e.AtMS = ms(e.At.Sub(r.start))
	r.events = append(r.events, e)
}
func (r *recorder) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GetConn: func(host string) { r.add(Event{Name: "GetConn", Host: host}) },
		GotConn: func(info httptrace.GotConnInfo) {
			reused, idle, idleMS := info.Reused, info.WasIdle, ms(info.IdleTime)
			e := Event{Name: "GotConn", Reused: &reused, WasIdle: &idle, IdleMS: &idleMS}
			if info.Conn != nil {
				e.LocalAddress = info.Conn.LocalAddr().String()
				e.RemoteAddress = info.Conn.RemoteAddr().String()
			}
			r.add(e)
		},
		DNSStart: func(info httptrace.DNSStartInfo) { r.add(Event{Name: "DNSStart", Host: info.Host}) },
		DNSDone: func(info httptrace.DNSDoneInfo) {
			addresses := make([]string, 0, len(info.Addrs))
			for _, a := range info.Addrs {
				addresses = append(addresses, a.String())
			}
			coalesced := info.Coalesced
			r.add(Event{Name: "DNSDone", DNSAddresses: addresses, DNSCoalesced: &coalesced, Error: errText(info.Err)})
		},
		ConnectStart: func(network, address string) { r.add(Event{Name: "ConnectStart", Network: network, Address: address}) },
		ConnectDone: func(network, address string, err error) {
			r.add(Event{Name: "ConnectDone", Network: network, Address: address, Error: errText(err)})
		},
		TLSHandshakeStart: func() { r.add(Event{Name: "TLSHandshakeStart"}) },
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			r.add(Event{Name: "TLSHandshakeDone", TLSVersion: state.Version, ALPN: state.NegotiatedProtocol, Error: errText(err)})
		},
		WroteRequest:         func(info httptrace.WroteRequestInfo) { r.add(Event{Name: "WroteRequest", Error: errText(info.Err)}) },
		GotFirstResponseByte: func() { r.add(Event{Name: "GotFirstResponseByte"}) },
		PutIdleConn:          func(err error) { r.add(Event{Name: "PutIdleConn", Error: errText(err)}) },
	}
}
func (r *recorder) snapshot() ([]Event, float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...), ms(time.Since(r.start))
}

// These diagnostic spans overlap: acquire includes DNS, TCP and TLS when a
// new connection is needed. Never add the phase durations together.
// DNS/TLS callbacks have no attempt ID; FIFO pairing is only a convenience.
// The raw event sequence is the authoritative record for complex retries.
func deriveIntervals(events []Event) []Interval {
	result := make([]Interval, 0)
	pending := make(map[string][]Event)
	for _, e := range events {
		var phase, key string
		start := false
		switch e.Name {
		case "GetConn":
			phase = "acquire"
			start = true
			delete(pending, "request_to_first_byte")
		case "GotConn":
			phase = "acquire"
		case "DNSStart":
			phase = "dns"
			start = true
		case "DNSDone":
			phase = "dns"
		case "ConnectStart":
			phase = "connect"
			start = true
		case "ConnectDone":
			phase = "connect"
		case "TLSHandshakeStart":
			phase = "tls"
			start = true
		case "TLSHandshakeDone":
			phase = "tls"
		case "WroteRequest":
			if e.Error != "" {
				continue
			}
			phase = "request_to_first_byte"
			start = true
		case "GotFirstResponseByte":
			phase = "request_to_first_byte"
		default:
			continue
		}
		if phase == "connect" && !strings.HasPrefix(e.Network, "tcp") {
			phase = "resolver_socket"
		}
		key = phase
		if phase == "connect" || phase == "resolver_socket" {
			key += "\x00" + e.Network + "\x00" + e.Address
		}
		if start {
			pending[key] = append(pending[key], e)
			continue
		}
		queue := pending[key]
		if len(queue) == 0 {
			continue
		}
		first := queue[0]
		pending[key] = queue[1:]
		if e.AtMS < first.AtMS {
			continue
		}
		result = append(result, Interval{Phase: phase, StartMS: first.AtMS, EndMS: e.AtMS, DurationMS: e.AtMS - first.AtMS, Network: e.Network, Address: e.Address, Error: e.Error})
	}
	return result
}
