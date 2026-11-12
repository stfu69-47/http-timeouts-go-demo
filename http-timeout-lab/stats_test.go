package main

import "testing"

// A mean/interpolated percentile would hide the deliberately rare slow tail.
func TestNearestRankPreservesRareTail(t *testing.T) {
	values := make([]float64, 100)
	for i := range values {
		values[i] = 5
	}
	values[98], values[99] = 305, 310
	d := distribution(values)
	if d.N != 100 || d.P50 == nil || *d.P50 != 5 || *d.P95 != 5 || *d.P99 != 305 || *d.Max != 310 {
		t.Fatalf("wrong nearest-rank result: %+v", d)
	}
	if empty := distribution(nil); empty.N != 0 || empty.P50 != nil || empty.P99 != nil {
		t.Fatalf("missing samples must remain null: %+v", empty)
	}
	if one := distribution([]float64{7}); one.P99 == nil || *one.P99 != 7 {
		t.Fatalf("single-sample quantile: %+v", one)
	}
}

// Dropping failed/time-limited requests must change the all-request denominator.
func TestSummaryKeepsErrorsInLatencyDenominator(t *testing.T) {
	rows := []RequestRecord{{DurationMS: 5, Status: 200}, {DurationMS: 50, BodyError: "deadline", TimedOut: true, Status: 200}, {DurationMS: 70, DoError: "dial timeout", TimedOut: true}, {DurationMS: 10, Status: 503}}
	s := summarize("test", rows)
	if s.Requests != 4 || s.Completed != 2 || s.IOErrors != 2 || s.Timeouts != 2 || s.BodyErrors != 1 || s.DoErrors != 1 || s.HTTPErrorResponses != 1 {
		t.Fatalf("wrong counters: %+v", s)
	}
	if s.LatencyAll.N != 4 || s.LatencyCompleted.N != 2 || s.LatencyErrors.N != 2 || s.LatencyAll.P99 == nil || *s.LatencyAll.P99 != 70 {
		t.Fatalf("wrong latency denominators: %+v", s)
	}
	if s.Phases["dns"].P50 != nil {
		t.Fatal("absent DNS interval became zero")
	}
}
