package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testClient() (*http.Client, *http.Transport) {
	return newClient(ClientConfig{Concurrency: 2, MaxConns: 2, Timeout: time.Second, DialTimeout: time.Second, TLSTimeout: time.Second, HeaderTimeout: time.Second})
}

// Checking only Client.Do would incorrectly count this truncated response as success.
func TestMeasureCountsBodyReadFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "8")
		w.WriteHeader(200)
		w.Write([]byte("x"))
	}))
	defer srv.Close()
	c, tr := testClient()
	defer tr.CloseIdleConnections()
	r := measure(context.Background(), c, srv.URL, "truncated", 1, 0)
	if r.DoError != "" || r.BodyError == "" || r.Status != 200 || r.Bytes != 1 || r.BodyMS == nil || r.HeadersMS == nil {
		t.Fatalf("body error was lost: %+v", r)
	}
	if r.DurationMS < *r.HeadersMS+*r.BodyMS {
		t.Fatalf("wall time ended before body Close: %+v", r)
	}
}

// A client deadline after flushed headers belongs to Body.Read, not Client.Do.
func TestMeasureCountsBodyTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "8")
		w.Write([]byte("x"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	c, tr := testClient()
	c.Timeout = 50 * time.Millisecond
	defer tr.CloseIdleConnections()
	r := measure(context.Background(), c, srv.URL, "body-timeout", 1, 0)
	if r.DoError != "" || r.BodyError == "" || !r.TimedOut {
		t.Fatalf("timeout stage wrong: %+v", r)
	}
	s := summarize("body-timeout", []RequestRecord{r})
	if s.Requests != 1 || s.IOErrors != 1 || s.BodyErrors != 1 || s.Timeouts != 1 || s.LatencyAll.N != 1 || s.LatencyCompleted.N != 0 {
		t.Fatalf("timeout denominator wrong: %+v", s)
	}
}

// Not reading/closing the body or rebuilding transports would prevent socket reuse.
func TestSharedTransportReusesRealConnection(t *testing.T) {
	for _, noReuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "reuse", true: "no-reuse"}[noReuse], func(t *testing.T) {
			var connections atomic.Int64
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
			srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
				if s == http.StateNew {
					connections.Add(1)
				}
			}
			srv.Start()
			defer srv.Close()
			c, tr := newClient(ClientConfig{Concurrency: 1, MaxConns: 1, NoReuse: noReuse, Timeout: time.Second})
			defer tr.CloseIdleConnections()
			var rows []RequestRecord
			for i := 1; i <= 3; i++ {
				rows = append(rows, measure(context.Background(), c, srv.URL, "reuse", i, 0))
			}
			s := summarize("reuse", rows)
			wantConnections := int64(1)
			wantReuse := 2
			if noReuse {
				wantConnections = 3
				wantReuse = 0
			}
			if connections.Load() != wantConnections || s.ReusedConnections != wantReuse || s.Completed != 3 || rows[0].Protocol != "HTTP/1.1" {
				t.Fatalf("reuse mismatch: sockets=%d summary=%+v", connections.Load(), s)
			}
		})
	}
}

// Following redirects hides multiple RoundTrips inside one record.
func TestRedirectIsReturnedWithoutFollowing(t *testing.T) {
	var followed atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			followed.Add(1)
			w.Write([]byte("target"))
			return
		}
		http.Redirect(w, r, "/target", http.StatusFound)
	}))
	defer srv.Close()
	c, tr := testClient()
	defer tr.CloseIdleConnections()
	r := measure(context.Background(), c, srv.URL, "redirect", 1, 0)
	if r.Status != 302 || followed.Load() != 0 || r.failed() {
		t.Fatalf("redirect was followed or failed: %+v, followed=%d", r, followed.Load())
	}
}

func TestHeaderDeadlineIsDoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	c, tr := testClient()
	tr.ResponseHeaderTimeout = 30 * time.Millisecond
	defer tr.CloseIdleConnections()
	r := measure(context.Background(), c, srv.URL, "header-timeout", 1, 0)
	if r.DoError == "" || r.BodyError != "" || !r.TimedOut || r.BodyMS != nil || r.HeadersMS != nil {
		t.Fatalf("header deadline misclassified: %+v", r)
	}
}

func TestRunRequestsProducesEveryIDOnce(t *testing.T) {
	var received atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); w.Write([]byte("ok")) }))
	defer srv.Close()
	c, tr := testClient()
	defer tr.CloseIdleConnections()
	rows, err := runRequests(context.Background(), c, RunConfig{Name: "batch", URL: srv.URL, N: 37, Concurrency: 5})
	if err != nil || len(rows) != 37 || received.Load() != 37 {
		t.Fatalf("lost workload requests: len=%d received=%d err=%v", len(rows), received.Load(), err)
	}
	for i, r := range rows {
		if r.ID != i+1 || r.failed() {
			t.Fatalf("invalid row %d: %+v", i, r)
		}
	}
}
