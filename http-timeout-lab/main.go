package main

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if err := runCLI(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
func runCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: timeoutlab suite | probe | serve (use -h after a command)")
	}
	switch args[0] {
	case "suite":
		return runSuite(args[1:])
	case "probe":
		return runProbe(args[1:])
	case "serve":
		return runServe(args[1:])
	default:
		return fmt.Errorf("unknown command %q; use suite, probe or serve", args[0])
	}
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
func writeRecords(w io.Writer, rows []RequestRecord) error {
	b := bufio.NewWriter(w)
	encoder := json.NewEncoder(b)
	for _, r := range rows {
		if err := encoder.Encode(r); err != nil {
			return err
		}
	}
	return b.Flush()
}
func writeRecordFile(path string, rows []RequestRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	writeErr := writeRecords(f, rows)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func runProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	target := fs.String("url", "http://127.0.0.1:8080/", "HTTP(S) URL (redirects are not followed)")
	n := fs.Int("n", 200, "number of measured requests")
	concurrency := fs.Int("c", 8, "closed-loop worker count")
	maxConns := fs.Int("max-conns", 0, "MaxConnsPerHost (0 means no connection cap)")
	timeout := fs.Duration("timeout", 2*time.Second, "http.Client timeout, including response body (0 disables)")
	dialTimeout := fs.Duration("dial-timeout", time.Second, "net.Dialer timeout (0 disables)")
	tlsTimeout := fs.Duration("tls-timeout", time.Second, "TLS handshake timeout (0 disables)")
	headerTimeout := fs.Duration("header-timeout", time.Second, "response header timeout (0 disables)")
	requestTimeout := fs.Duration("request-timeout", 0, "per-request context deadline (0 disables)")
	noReuse := fs.Bool("no-reuse", false, "disable HTTP connection reuse")
	proxyEnv := fs.Bool("proxy-env", false, "honor HTTP_PROXY, HTTPS_PROXY and NO_PROXY; default is direct")
	dnsServer := fs.String("dns-server", "", "optional DNS server host:port, using Go resolver")
	caFile := fs.String("ca-file", "", "optional additional trusted CA certificates in PEM format")
	name := fs.String("name", "probe", "scenario name included in every record")
	out := fs.String("out", "-", "JSONL path, or - for stdout; a file gets a .summary.json sibling")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	if *n < 1 || *concurrency < 1 || *maxConns < 0 {
		return fmt.Errorf("n and c must be positive; max-conns must be nonnegative")
	}
	for _, d := range []time.Duration{*timeout, *dialTimeout, *tlsTimeout, *headerTimeout, *requestTimeout} {
		if d < 0 {
			return fmt.Errorf("timeouts must be nonnegative")
		}
	}
	u, err := url.Parse(*target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("url must be an absolute http:// or https:// URL")
	}
	cfg := ClientConfig{Concurrency: *concurrency, MaxConns: *maxConns, Timeout: *timeout, DialTimeout: *dialTimeout, TLSTimeout: *tlsTimeout, HeaderTimeout: *headerTimeout, NoReuse: *noReuse, ProxyEnv: *proxyEnv}
	if *dnsServer != "" {
		if _, _, err := net.SplitHostPort(*dnsServer); err != nil {
			return fmt.Errorf("dns-server: %w", err)
		}
		cfg.Resolver = resolverAt(*dnsServer)
	}
	if *caFile != "" {
		pem, err := os.ReadFile(*caFile)
		if err != nil {
			return err
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return fmt.Errorf("ca-file contains no certificates")
		}
		cfg.RootCAs = roots
	}
	client, transport := newClient(cfg)
	defer transport.CloseIdleConnections()
	rows, err := runRequests(context.Background(), client, RunConfig{Name: *name, URL: *target, N: *n, Concurrency: *concurrency, RequestTimeout: *requestTimeout})
	if err != nil {
		return err
	}
	summary := summarize(*name, rows)
	if *out == "-" {
		if err := writeRecords(os.Stdout, rows); err != nil {
			return err
		}
		return json.NewEncoder(os.Stderr).Encode(summary)
	}
	if err := writeRecordFile(*out, rows); err != nil {
		return err
	}
	if err := writeJSON(*out+".summary.json", summary); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(summary)
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:8080", "HTTP listen address")
	hd := fs.Duration("header-delay", 5*time.Millisecond, "synthetic delay before response headers")
	bd := fs.Duration("body-delay", 0, "synthetic delay after one flushed response-body byte")
	every := fs.Int("slow-every", 0, "delay every Nth positive X-Lab-Request-ID (0 disables)")
	slowDelay := fs.Duration("slow-delay", 300*time.Millisecond, "extra delay for selected requests")
	slowStage := fs.String("slow-stage", "headers", "extra delay stage: headers or body")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	if *hd < 0 || *bd < 0 || *slowDelay < 0 || *every < 0 || (*slowStage != "headers" && *slowStage != "body") {
		return fmt.Errorf("delays and slow-every must be nonnegative; slow-stage is headers or body")
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: fixtureHandler(FixtureConfig{HeaderDelay: *hd, BodyDelay: *bd, SlowEvery: *every, SlowDelay: *slowDelay, SlowStage: *slowStage}), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fmt.Fprintf(os.Stderr, "HTTP/1.1 synthetic fixture listening on http://%s\n", listener.Addr())
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
