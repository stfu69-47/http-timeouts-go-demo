package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type ClientConfig struct {
	Concurrency, MaxConns                           int
	NoReuse, ProxyEnv                               bool
	Timeout, DialTimeout, TLSTimeout, HeaderTimeout time.Duration
	RootCAs                                         *x509.CertPool
	Resolver                                        *net.Resolver
}

func newClient(cfg ClientConfig) (*http.Client, *http.Transport) {
	idle := cfg.Concurrency
	if idle < 1 {
		idle = 1
	}
	dialer := &net.Dialer{Timeout: cfg.DialTimeout, KeepAlive: 30 * time.Second, Resolver: cfg.Resolver}
	t := &http.Transport{DialContext: dialer.DialContext, MaxConnsPerHost: cfg.MaxConns, MaxIdleConns: 2 * idle, MaxIdleConnsPerHost: idle, IdleConnTimeout: 90 * time.Second, DisableKeepAlives: cfg.NoReuse, TLSHandshakeTimeout: cfg.TLSTimeout, ResponseHeaderTimeout: cfg.HeaderTimeout,
		ForceAttemptHTTP2: false, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: cfg.RootCAs, NextProtos: []string{"http/1.1"}}}
	if cfg.ProxyEnv {
		t.Proxy = http.ProxyFromEnvironment
	}
	c := &http.Client{Transport: t, Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return c, t
}

func timeoutError(err error) bool {
	var ne net.Error
	return err != nil && (errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()))
}

func measure(ctx context.Context, client *http.Client, url, scenario string, id int, requestTimeout time.Duration) RequestRecord {
	start := time.Now()
	rec := newRecorder(start)
	r := RequestRecord{Scenario: scenario, ID: id, StartedAt: start, Method: http.MethodGet, URL: url}
	finish := func() RequestRecord {
		rec.add(Event{Name: "RequestDone"})
		r.DurationMS = ms(time.Since(start))
		r.Events, r.TraceSnapshotMS = rec.snapshot()
		r.Intervals = deriveIntervals(r.Events)
		return r
	}
	if requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, requestTimeout)
		defer cancel()
	}
	ctx = httptrace.WithClientTrace(ctx, rec.trace())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		r.DoError = err.Error()
		return finish()
	}
	req.Header.Set("X-Lab-Request-ID", strconv.Itoa(id))
	resp, doErr := client.Do(req)
	doDone := time.Now()
	rec.add(Event{Name: "DoReturned", Error: errText(doErr)})
	r.DoError = errText(doErr)
	r.TimedOut = timeoutError(doErr)
	if resp != nil {
		r.Status = resp.StatusCode
		r.Protocol = resp.Proto
		r.FixtureSlow = resp.Header.Get("X-Lab-Slow") == "true"
		if doErr == nil {
			h := ms(doDone.Sub(start))
			r.HeadersMS = &h
		}
		if resp.Body != nil {
			if doErr == nil {
				var bodyErr error
				r.Bytes, bodyErr = io.Copy(io.Discard, resp.Body)
				r.BodyError = errText(bodyErr)
				r.TimedOut = r.TimedOut || timeoutError(bodyErr)
				name := "BodyEOF"
				if bodyErr != nil {
					name = "BodyReadError"
				}
				rec.add(Event{Name: name, Error: errText(bodyErr)})
			}
			closeErr := resp.Body.Close()
			r.CloseError = errText(closeErr)
			r.TimedOut = r.TimedOut || timeoutError(closeErr)
			rec.add(Event{Name: "BodyClosed", Error: errText(closeErr)})
			if doErr == nil {
				b := ms(time.Since(doDone))
				r.BodyMS = &b
			}
		}
	}
	return finish()
}

type RunConfig struct {
	Name           string        `json:"name"`
	URL            string        `json:"url"`
	N              int           `json:"n"`
	Concurrency    int           `json:"concurrency"`
	RequestTimeout time.Duration `json:"request_timeout_ns"`
}

// Every worker starts the next request only after its previous body is read and
// closed. This is a bounded, closed-loop diagnostic, not a capacity benchmark.
func runRequests(ctx context.Context, client *http.Client, cfg RunConfig) ([]RequestRecord, error) {
	if cfg.N < 1 || cfg.Concurrency < 1 {
		return nil, fmt.Errorf("n and concurrency must be positive")
	}
	rows := make([]RequestRecord, cfg.N)
	var next atomic.Int64
	var wg sync.WaitGroup
	workers := cfg.Concurrency
	if workers > cfg.N {
		workers = cfg.N
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				id := int(next.Add(1))
				if id > cfg.N {
					return
				}
				rows[id-1] = measure(ctx, client, cfg.URL, cfg.Name, id, cfg.RequestTimeout)
			}
		}()
	}
	wg.Wait()
	return rows, nil
}
