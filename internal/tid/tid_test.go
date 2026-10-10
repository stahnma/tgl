package tid

import (
	"testing"
	"time"
)

func TestFromTimeRoundTrip(t *testing.T) {
	want := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	s := FromTime(want)
	if len(s) != Length {
		t.Fatalf("FromTime produced %d chars, want %d", len(s), Length)
	}
	if !Valid(s) {
		t.Fatalf("FromTime produced an invalid TID: %q", s)
	}
	got := Time(s)
	if !got.Equal(want) {
		t.Errorf("Time(FromTime(%v)) = %v", want, got)
	}
}

func TestMonotonic(t *testing.T) {
	// Two TIDs minted in the same microsecond must still sort strictly, since
	// rkeys are the sort key for creation order.
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	prev := ""
	for i := 0; i < 100; i++ {
		s := FromTime(fixed)
		if s <= prev {
			t.Fatalf("TID %d (%q) does not sort after %q", i, s, prev)
		}
		prev = s
	}
}

func TestValid(t *testing.T) {
	valid := []string{"3ms4qpm3qjd22", "3mdy4uko2vk22", "3lqxgkqw26l22"}
	for _, s := range valid {
		if !Valid(s) {
			t.Errorf("Valid(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "short", "3ms4qpm3qjd2", "3ms4qpm3qjd221", "3ms4qpm3qjd21" /* 1 is not in the alphabet */, "ZZZZZZZZZZZZZ"}
	for _, s := range invalid {
		if Valid(s) {
			t.Errorf("Valid(%q) = true, want false", s)
		}
	}
}

func TestOrderMatchesTime(t *testing.T) {
	a := FromTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	b := FromTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if !(a < b) {
		t.Errorf("earlier TID %q should sort before later TID %q", a, b)
	}
}
