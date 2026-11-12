package main

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// A missing mutex or an overwritten singleton event loses concurrent dial attempts.
func TestRecorderPreservesConcurrentDialEvents(t *testing.T) {
	r := newRecorder(time.Now())
	tr := r.trace()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			addr := fmt.Sprintf("127.0.0.1:%d", i)
			tr.ConnectStart("tcp", addr)
			tr.ConnectDone("tcp", addr, errors.New("refused"))
		}(i)
	}
	wg.Wait()
	events, _ := r.snapshot()
	if len(events) != 128 {
		t.Fatalf("lost callbacks: got %d want 128", len(events))
	}
	intervals := deriveIntervals(events)
	if len(intervals) != 64 {
		t.Fatalf("lost attempts: got %d want 64", len(intervals))
	}
	for _, iv := range intervals {
		if iv.Error != "refused" || iv.Address == "" || iv.DurationMS < 0 {
			t.Fatalf("bad dial interval: %+v", iv)
		}
	}
}

// Address-aware matching avoids treating parallel IPv4/IPv6 dials as one interval.
func TestIntervalsRetainAttemptsAndMissingPhases(t *testing.T) {
	e := []Event{{Name: "GetConn", AtMS: 0}, {Name: "ConnectStart", AtMS: 1, Network: "tcp", Address: "v6"}, {Name: "ConnectStart", AtMS: 4, Network: "tcp", Address: "v4"}, {Name: "ConnectDone", AtMS: 8, Network: "tcp", Address: "v4"}, {Name: "ConnectDone", AtMS: 10, Network: "tcp", Address: "v6", Error: "refused"}, {Name: "GotConn", AtMS: 11}, {Name: "WroteRequest", AtMS: 12}, {Name: "GotFirstResponseByte", AtMS: 20}, {Name: "GetConn", AtMS: 25}, {Name: "GotConn", AtMS: 25}}
	ivs := deriveIntervals(e)
	want := map[string][]float64{"acquire": {11, 0}, "connect": {4, 9}, "request_to_first_byte": {8}}
	got := map[string][]float64{}
	for _, iv := range ivs {
		got[iv.Phase] = append(got[iv.Phase], iv.DurationMS)
	}
	for phase, values := range want {
		if len(got[phase]) != len(values) {
			t.Fatalf("phase %s: %+v", phase, got)
		}
		for i, v := range values {
			if got[phase][i] != v {
				t.Fatalf("phase %s: %+v", phase, got)
			}
		}
	}
	if len(got["dns"]) != 0 || len(got["tls"]) != 0 {
		t.Fatalf("absent phases must not be invented: %+v", got)
	}
}

func TestEarlyResponseAndRetryDoNotInventTTFB(t *testing.T) {
	events := []Event{{Name: "GotFirstResponseByte", AtMS: 1}, {Name: "WroteRequest", AtMS: 2}, {Name: "GetConn", AtMS: 3}, {Name: "WroteRequest", AtMS: 4}, {Name: "GotFirstResponseByte", AtMS: 6}}
	ivs := deriveIntervals(events)
	if len(ivs) != 1 || ivs[0].Phase != "request_to_first_byte" || ivs[0].DurationMS != 2 {
		t.Fatalf("invalid early-response or retry interval: %+v", ivs)
	}
}

// The custom resolver opens UDP sockets; those callbacks must not pollute TCP latency.
func TestResolverSocketIntervalsAreSeparate(t *testing.T) {
	events := []Event{{Name: "ConnectStart", Network: "udp", Address: "dns", AtMS: 0}, {Name: "ConnectDone", Network: "udp", Address: "dns", AtMS: 1}, {Name: "ConnectStart", Network: "tcp", Address: "origin", AtMS: 2}, {Name: "ConnectDone", Network: "tcp", Address: "origin", AtMS: 4}}
	ivs := deriveIntervals(events)
	if len(ivs) != 2 || ivs[0].Phase != "resolver_socket" || ivs[1].Phase != "connect" {
		t.Fatalf("DNS socket polluted TCP intervals: %+v", ivs)
	}
}
