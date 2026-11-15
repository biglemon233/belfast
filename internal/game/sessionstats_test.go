package game

import (
	"testing"
	"time"
)

func TestSameDay(t *testing.T) {
	start := time.Date(2026, 11, 15, 0, 0, 0, 0, time.Local)
	if !sameDay(start, start.Add(23*time.Hour)) {
		t.Fatalf("two instants of the same day must compare equal")
	}
	if sameDay(start, start.Add(24*time.Hour)) {
		t.Fatalf("next day must open a new window")
	}
}
