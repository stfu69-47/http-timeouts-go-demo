package main

import (
	"math"
	"sort"
)

func distribution(values []float64) Distribution {
	d := Distribution{N: len(values)}
	if len(values) == 0 {
		return d
	}
	x := append([]float64(nil), values...)
	sort.Float64s(x)
	at := func(p float64) *float64 {
		i := int(math.Ceil(p*float64(len(x)))) - 1
		if i < 0 {
			i = 0
		}
		v := x[i]
		return &v
	}
	d.Min = at(0)
	d.P50 = at(.50)
	d.P95 = at(.95)
	d.P99 = at(.99)
	d.Max = at(1)
	return d
}

func summarize(name string, records []RequestRecord) Summary {
	s := Summary{Scenario: name, Requests: len(records), StatusCodes: make(map[int]int), Phases: make(map[string]Distribution)}
	var all, completed, failed, headers, body []float64
	phases := map[string][]float64{"acquire": nil, "dns": nil, "connect": nil, "tls": nil, "request_to_first_byte": nil, "resolver_socket": nil}
	for _, r := range records {
		all = append(all, r.DurationMS)
		if r.failed() {
			s.IOErrors++
			failed = append(failed, r.DurationMS)
		} else {
			s.Completed++
			completed = append(completed, r.DurationMS)
		}
		if r.DoError != "" {
			s.DoErrors++
		}
		if r.BodyError != "" {
			s.BodyErrors++
		}
		if r.CloseError != "" {
			s.CloseErrors++
		}
		if r.TimedOut {
			s.Timeouts++
		}
		if r.Status >= 400 {
			s.HTTPErrorResponses++
		}
		if r.Status > 0 {
			s.StatusCodes[r.Status]++
		}
		if r.FixtureSlow {
			s.FixtureSlowResponses++
		}
		if r.HeadersMS != nil {
			headers = append(headers, *r.HeadersMS)
		}
		if r.BodyMS != nil {
			body = append(body, *r.BodyMS)
		}
		for _, e := range r.Events {
			if e.Name == "GotConn" && e.Reused != nil {
				if *e.Reused {
					s.ReusedConnections++
				} else {
					s.NewConnections++
				}
			}
		}
		for _, iv := range r.Intervals {
			phases[iv.Phase] = append(phases[iv.Phase], iv.DurationMS)
		}
	}
	s.LatencyAll = distribution(all)
	s.LatencyCompleted = distribution(completed)
	s.LatencyErrors = distribution(failed)
	s.Headers = distribution(headers)
	s.Body = distribution(body)
	for k, v := range phases {
		s.Phases[k] = distribution(v)
	}
	return s
}
