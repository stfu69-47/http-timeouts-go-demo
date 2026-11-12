package main

import (
	"context"
	"crypto/x509"
	"encoding/csv"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"text/tabwriter"
	"time"
)

type experiment struct {
	Name          string `json:"name"`
	N             int    `json:"n"`
	Concurrency   int    `json:"concurrency"`
	MaxConns      int    `json:"max_conns"`
	NoReuse       bool   `json:"no_reuse"`
	HeaderDelay   string `json:"header_delay"`
	BodyDelay     string `json:"body_delay"`
	SlowEvery     int    `json:"slow_every"`
	SlowStage     string `json:"slow_stage"`
	SlowDelay     string `json:"slow_delay"`
	TLS           bool   `json:"tls"`
	TLSDelay      string `json:"tls_delay"`
	DNSDelay      string `json:"dns_delay"`
	ClientTimeout string `json:"client_timeout"`
	HeaderTimeout string `json:"header_timeout"`
}

func duration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic(err)
	}
	return d
}
func experiments() []experiment {
	base := experiment{N: 200, Concurrency: 8, MaxConns: 8, HeaderDelay: "5ms", BodyDelay: "0s", SlowDelay: "0s", TLSDelay: "0s", DNSDelay: "0s", ClientTimeout: "2s", HeaderTimeout: "1s"}
	makeExp := func(name string, n, c, max int) experiment {
		e := base
		e.Name = name
		e.N = n
		e.Concurrency = c
		e.MaxConns = max
		return e
	}
	baseline := makeExp("baseline", 200, 8, 8)
	headers := makeExp("rare_headers", 500, 8, 8)
	headers.SlowEvery = 50
	headers.SlowStage = "headers"
	headers.SlowDelay = "300ms"
	body := headers
	body.Name = "rare_body"
	body.SlowStage = "body"
	pool1 := makeExp("pool_1", 100, 8, 1)
	pool1.HeaderDelay = "25ms"
	pool8 := pool1
	pool8.Name = "pool_8"
	pool8.MaxConns = 8
	tlsReuse := makeExp("tls_reuse", 100, 1, 1)
	tlsReuse.TLS = true
	tlsReuse.TLSDelay = "30ms"
	tlsNoReuse := tlsReuse
	tlsNoReuse.Name = "tls_no_reuse"
	tlsNoReuse.NoReuse = true
	dns := makeExp("dns_delay", 50, 1, 1)
	dns.DNSDelay = "25ms"
	dns.NoReuse = true
	dns.HeaderDelay = "2ms"
	headerTimeout := makeExp("header_timeout", 20, 4, 4)
	headerTimeout.HeaderDelay = "150ms"
	headerTimeout.HeaderTimeout = "40ms"
	bodyTimeout := makeExp("body_timeout", 20, 4, 4)
	bodyTimeout.HeaderDelay = "0s"
	bodyTimeout.BodyDelay = "150ms"
	bodyTimeout.ClientTimeout = "50ms"
	return []experiment{baseline, headers, body, pool1, pool8, tlsReuse, tlsNoReuse, dns, headerTimeout, bodyTimeout}
}

func runExperiment(e experiment) ([]RequestRecord, error) {
	fixture := startFixture(FixtureConfig{HeaderDelay: duration(e.HeaderDelay), BodyDelay: duration(e.BodyDelay), SlowEvery: e.SlowEvery, SlowStage: e.SlowStage, SlowDelay: duration(e.SlowDelay), TLS: e.TLS, TLSDelay: duration(e.TLSDelay)})
	defer fixture.Close()
	cfg := ClientConfig{Concurrency: e.Concurrency, MaxConns: e.MaxConns, NoReuse: e.NoReuse, Timeout: duration(e.ClientTimeout), DialTimeout: time.Second, TLSTimeout: time.Second, HeaderTimeout: duration(e.HeaderTimeout)}
	target := fixture.URL
	if e.TLS {
		cfg.RootCAs = x509.NewCertPool()
		cfg.RootCAs.AddCert(fixture.Certificate())
	}
	if delay := duration(e.DNSDelay); delay > 0 {
		dns, err := startDNSFixture(delay)
		if err != nil {
			return nil, err
		}
		defer dns.Close()
		cfg.Resolver = dns.Resolver()
		u, err := url.Parse(target)
		if err != nil {
			return nil, err
		}
		_, port, err := net.SplitHostPort(u.Host)
		if err != nil {
			return nil, err
		}
		u.Host = net.JoinHostPort("fixture.test", port)
		target = u.String()
	}
	client, transport := newClient(cfg)
	defer transport.CloseIdleConnections()
	return runRequests(context.Background(), client, RunConfig{Name: e.Name, URL: target, N: e.N, Concurrency: e.Concurrency})
}

func number(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', 6, 64)
}
func writeCSV(path string, summaries []Summary) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	w.Write([]string{"scenario", "n", "completed", "io_errors", "timeouts", "p50_ms", "p95_ms", "p99_ms", "max_ms", "acquire_p95_ms", "dns_p95_ms", "tcp_p95_ms", "tls_p95_ms", "request_to_first_byte_p95_ms", "body_p95_ms", "new_conn_events", "reused_conn_events"})
	for _, s := range summaries {
		w.Write([]string{s.Scenario, strconv.Itoa(s.Requests), strconv.Itoa(s.Completed), strconv.Itoa(s.IOErrors), strconv.Itoa(s.Timeouts), number(s.LatencyAll.P50), number(s.LatencyAll.P95), number(s.LatencyAll.P99), number(s.LatencyAll.Max), number(s.Phases["acquire"].P95), number(s.Phases["dns"].P95), number(s.Phases["connect"].P95), number(s.Phases["tls"].P95), number(s.Phases["request_to_first_byte"].P95), number(s.Body.P95), strconv.Itoa(s.NewConnections), strconv.Itoa(s.ReusedConnections)})
	}
	w.Flush()
	writeErr := w.Error()
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func runSuite(args []string) error {
	fs := flag.NewFlagSet("suite", flag.ContinueOnError)
	out := fs.String("out", "results", "directory for measurements")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	started := time.Now()
	exps := experiments()
	summaries := make([]Summary, 0, len(exps))
	table := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(table, "scenario\tn\tIO errors\tp50 ms\tp95 ms\tp99 ms\tnew/reused")
	for _, e := range exps {
		rows, err := runExperiment(e)
		if err != nil {
			return fmt.Errorf("%s: %w", e.Name, err)
		}
		if err := writeRecordFile(filepath.Join(*out, e.Name+".jsonl"), rows); err != nil {
			return err
		}
		s := summarize(e.Name, rows)
		summaries = append(summaries, s)
		fmt.Fprintf(table, "%s\t%d\t%d\t%s\t%s\t%s\t%d/%d\n", e.Name, s.Requests, s.IOErrors, number(s.LatencyAll.P50), number(s.LatencyAll.P95), number(s.LatencyAll.P99), s.NewConnections, s.ReusedConnections)
		table.Flush()
	}
	if err := writeJSON(filepath.Join(*out, "summary.json"), summaries); err != nil {
		return err
	}
	if err := writeCSV(filepath.Join(*out, "summary.csv"), summaries); err != nil {
		return err
	}
	metadata := map[string]any{"schema_version": 1, "started_at": started.UTC(), "finished_at": time.Now().UTC(), "wall_ms": ms(time.Since(started)), "go_version": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "logical_cpus": runtime.NumCPU(), "gomaxprocs": runtime.GOMAXPROCS(0), "http_version": "HTTP/1.1", "workload": "closed loop; fixed workers; complete body read and Close before next request; all samples include cold start; no warmup", "quantiles": "exact nearest rank: sorted[ceil(p*n)-1]; all-request latency includes failures", "fixtures": "synthetic delays on localhost; all recorded durations are measured, not predicted", "phase_warning": "acquire includes connection establishment; phases overlap; phase sample count can differ from request count; missing percentiles are null", "trace_cutoff": "each request snapshots events just after body Close or Do error; callbacks arriving after snapshot are not included", "experiments": exps}
	return writeJSON(filepath.Join(*out, "metadata.json"), metadata)
}
