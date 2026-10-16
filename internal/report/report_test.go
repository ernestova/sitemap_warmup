package report

import (
	"reflect"
	"testing"

	"github.com/clarivo-co/sitewarm/internal/cdn"
	"github.com/clarivo-co/sitewarm/internal/probe"
	"github.com/clarivo-co/sitewarm/internal/verify"
)

func TestDefaultFailOn(t *testing.T) {
	f, err := ParseFailOn(DefaultFailOn)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"miss", "client-error", "error", "redirect-error", "incomplete", "noindex"}
	if !reflect.DeepEqual(f.List(), want) {
		t.Fatalf("default --fail-on = %v, want %v", f.List(), want)
	}
	cases := []struct {
		state cdn.State
		idx   probe.Indexability
		fails bool
	}{
		{cdn.Hit, probe.Indexable, false},
		{cdn.Miss, probe.Indexable, true},
		{cdn.ClientError, "", true},
		{cdn.Error, "", true},
		{cdn.RedirectError, "", true},
		{cdn.Incomplete, "", true},
		{cdn.Hit, probe.NoIndex, true},
		{cdn.Hit, probe.IndexabilityError, true},
		// Not failing by default: highlighted only.
		{cdn.Hit, probe.IndexabilityInconclusive, false},
		{cdn.Stale, probe.Indexable, false},
		{cdn.Bypass, probe.Indexable, false},
		{cdn.Unknown, probe.Indexable, false},
		{cdn.VariantUnverifiable, probe.Indexable, false},
		{cdn.Challenged, "", false},
	}
	for _, c := range cases {
		if got := f.Fails(c.state, c.idx); got != c.fails {
			t.Errorf("Fails(%s, %s) = %v, want %v", c.state, c.idx, got, c.fails)
		}
	}
}

func TestParseFailOn(t *testing.T) {
	f, err := ParseFailOn("bypass, indexability-inconclusive")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Fails(cdn.Bypass, "") || !f.Fails(cdn.Hit, probe.IndexabilityInconclusive) || f.Fails(cdn.Miss, "") {
		t.Fatalf("unexpected set %v", f.List())
	}
	if _, err := ParseFailOn("hit"); err == nil {
		t.Fatal("hit is not a failure state")
	}
	if _, err := ParseFailOn("bogus"); err == nil {
		t.Fatal("want error for unknown token")
	}
	if f, _ := ParseFailOn(""); len(f) != 0 {
		t.Fatal("empty --fail-on must fail on nothing")
	}
}

func TestBuildSummaryAndRedaction(t *testing.T) {
	failOn, _ := ParseFailOn(DefaultFailOn)
	hit := probe.Observation{Header: map[string][]string{"Set-Cookie": {"session=secret"}, "Cf-Cache-Status": {"HIT"}}, Status: 200, Indexability: probe.Indexable}
	outs := []verify.Outcome{
		{State: cdn.Hit, GotHit: true, Last: &hit, First: &hit},
		{State: cdn.Incomplete},
	}
	r := &Report{}
	Build(r, outs, failOn)
	if r.Summary.Total != 2 || r.Summary.Failed != 1 || r.Summary.Passed || r.Summary.HitRatio != 0.5 {
		t.Fatalf("summary = %+v", r.Summary)
	}
	if !r.Results[0].SetCookie {
		t.Fatal("Set-Cookie presence must be recorded")
	}
	if len(r.Warnings) != 2 {
		t.Fatalf("standing warnings missing: %v", r.Warnings)
	}
}
