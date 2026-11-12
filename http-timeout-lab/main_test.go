package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A successful command must persist every record and its denominator, not just print it.
func TestProbeWritesJSONLAndSummary(t *testing.T) {
	srv := startFixture(FixtureConfig{})
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "probe.jsonl")
	if err := runProbe([]string{"-url", srv.URL, "-n", "3", "-c", "1", "-out", out}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	n := 0
	for scanner.Scan() {
		var r RequestRecord
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if r.failed() || r.Bytes != 64 {
			t.Fatalf("invalid persisted request: %+v", r)
		}
		n++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("got %d records, want 3", n)
	}
	b, err := os.ReadFile(out + ".summary.json")
	if err != nil {
		t.Fatal(err)
	}
	var s Summary
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s.Requests != 3 || s.LatencyAll.N != 3 {
		t.Fatalf("invalid persisted summary: %+v", s)
	}
}
