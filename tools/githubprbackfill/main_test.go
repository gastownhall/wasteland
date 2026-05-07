package main

import (
	"testing"
	"time"
)

func TestParseMergedAfter(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	t.Run("date", func(t *testing.T) {
		got, err := parseMergedAfter("2026-05-01", 0, now)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Fatalf("got %s, want %s", got, want)
		}
	})

	t.Run("since days", func(t *testing.T) {
		got, err := parseMergedAfter("", 3, now)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Fatalf("got %s, want %s", got, want)
		}
	})

	t.Run("mutual exclusion", func(t *testing.T) {
		if _, err := parseMergedAfter("2026-05-01", 3, now); err == nil {
			t.Fatal("expected error")
		}
	})
}
