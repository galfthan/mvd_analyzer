package main

import (
	"net/url"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/view"
)

func TestUnitsParam(t *testing.T) {
	cases := []struct {
		q       string
		def     view.TimeUnit
		want    view.TimeUnit
		wantErr bool
	}{
		{"", view.UnitMs, view.UnitMs, false},               // absent → native default (ms endpoint)
		{"", view.UnitSec, view.UnitSec, false},             // absent → native default (seconds endpoint)
		{"units=ms", view.UnitSec, view.UnitMs, false},      // force ms on a seconds-native endpoint
		{"units=s", view.UnitMs, view.UnitSec, false},       // force seconds on an ms-native endpoint
		{"units=MS", view.UnitSec, view.UnitMs, false},      // case-insensitive
		{"units=%20s%20", view.UnitMs, view.UnitSec, false}, // trimmed
		{"units=seconds", view.UnitMs, view.UnitMs, true},   // only ms|s accepted
		{"units=1", view.UnitMs, view.UnitMs, true},
	}
	for _, tc := range cases {
		q, _ := url.ParseQuery(tc.q)
		p := newQP(q)
		got := p.Units(tc.def)
		if (p.Err() != nil) != tc.wantErr {
			t.Errorf("q=%q: err=%v, wantErr=%v", tc.q, p.Err(), tc.wantErr)
		}
		if got != tc.want {
			t.Errorf("q=%q: got %q, want %q", tc.q, got, tc.want)
		}
	}
}
