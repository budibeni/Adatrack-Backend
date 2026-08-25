package main

import (
	"testing"
	"time"
)

func TestPartitionEndMonthly(t *testing.T) {
	cases := map[string]string{
		"p_2027_01": "2027-02-01",
		"p_2026_12": "2027-01-01",
	}
	for name, want := range cases {
		got := partitionEnd(name)
		w, _ := time.Parse("2006-01-02", want)
		if !got.Equal(w) {
			t.Errorf("%s → %v, ingin %v", name, got.Format("2006-01-02"), want)
		}
	}
}

func TestPartitionEndQuarterly(t *testing.T) {
	cases := map[string]string{
		"p_2025_Q4": "2026-01-01",
		"p_2026_Q1": "2026-04-01",
		"p_2026_Q2": "2026-07-01",
		"p_2026_Q3": "2026-10-01",
	}
	for name, want := range cases {
		got := partitionEnd(name)
		w, _ := time.Parse("2006-01-02", want)
		if !got.Equal(w) {
			t.Errorf("%s → %v, ingin %v", name, got.Format("2006-01-02"), want)
		}
	}
}

func TestPartitionEndUnknown(t *testing.T) {
	if got := partitionEnd("p_future"); !got.IsZero() {
		t.Errorf("p_future harus zero-time, dapat %v", got)
	}
}
