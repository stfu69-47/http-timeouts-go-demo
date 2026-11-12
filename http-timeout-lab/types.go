package main

import "time"

type Event struct {
	Name          string    `json:"name"`
	At            time.Time `json:"at"`
	AtMS          float64   `json:"at_ms"`
	Host          string    `json:"host,omitempty"`
	Network       string    `json:"network,omitempty"`
	Address       string    `json:"address,omitempty"`
	Error         string    `json:"error,omitempty"`
	Reused        *bool     `json:"reused,omitempty"`
	WasIdle       *bool     `json:"was_idle,omitempty"`
	IdleMS        *float64  `json:"idle_ms,omitempty"`
	DNSAddresses  []string  `json:"dns_addresses,omitempty"`
	DNSCoalesced  *bool     `json:"dns_coalesced,omitempty"`
	LocalAddress  string    `json:"local_address,omitempty"`
	RemoteAddress string    `json:"remote_address,omitempty"`
	TLSVersion    uint16    `json:"tls_version,omitempty"`
	ALPN          string    `json:"alpn,omitempty"`
}

type Interval struct {
	Phase      string  `json:"phase"`
	StartMS    float64 `json:"start_ms"`
	EndMS      float64 `json:"end_ms"`
	DurationMS float64 `json:"duration_ms"`
	Network    string  `json:"network,omitempty"`
	Address    string  `json:"address,omitempty"`
	Error      string  `json:"error,omitempty"`
}

type RequestRecord struct {
	Scenario        string     `json:"scenario"`
	ID              int        `json:"id"`
	StartedAt       time.Time  `json:"started_at"`
	Method          string     `json:"method"`
	URL             string     `json:"url"`
	Status          int        `json:"status"`
	Protocol        string     `json:"protocol,omitempty"`
	Bytes           int64      `json:"body_bytes"`
	DurationMS      float64    `json:"duration_ms"`
	HeadersMS       *float64   `json:"headers_ms"`
	BodyMS          *float64   `json:"body_ms"`
	DoError         string     `json:"do_error,omitempty"`
	BodyError       string     `json:"body_error,omitempty"`
	CloseError      string     `json:"close_error,omitempty"`
	TimedOut        bool       `json:"timed_out"`
	FixtureSlow     bool       `json:"fixture_slow"`
	Events          []Event    `json:"events"`
	Intervals       []Interval `json:"intervals"`
	TraceSnapshotMS float64    `json:"trace_snapshot_ms"`
}

func (r RequestRecord) failed() bool {
	return r.DoError != "" || r.BodyError != "" || r.CloseError != ""
}

type Distribution struct {
	N   int      `json:"n"`
	Min *float64 `json:"min_ms"`
	P50 *float64 `json:"p50_ms"`
	P95 *float64 `json:"p95_ms"`
	P99 *float64 `json:"p99_ms"`
	Max *float64 `json:"max_ms"`
}

type Summary struct {
	Scenario             string                  `json:"scenario"`
	Requests             int                     `json:"requests"`
	Completed            int                     `json:"completed_responses"`
	IOErrors             int                     `json:"io_errors"`
	DoErrors             int                     `json:"do_errors"`
	BodyErrors           int                     `json:"body_errors"`
	CloseErrors          int                     `json:"close_errors"`
	Timeouts             int                     `json:"timeouts"`
	HTTPErrorResponses   int                     `json:"http_error_responses"`
	StatusCodes          map[int]int             `json:"status_codes"`
	NewConnections       int                     `json:"got_conn_new_events"`
	ReusedConnections    int                     `json:"got_conn_reused_events"`
	FixtureSlowResponses int                     `json:"fixture_slow_responses"`
	LatencyAll           Distribution            `json:"latency_all"`
	LatencyCompleted     Distribution            `json:"latency_completed"`
	LatencyErrors        Distribution            `json:"latency_errors"`
	Headers              Distribution            `json:"headers"`
	Body                 Distribution            `json:"body"`
	Phases               map[string]Distribution `json:"phases"`
}
