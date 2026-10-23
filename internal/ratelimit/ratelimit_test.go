package ratelimit

import (
	"testing"
	"time"
)

func TestAllowBurstUpToCapacity(t *testing.T) {
	orig := now
	now = func() time.Time { return time.Unix(1000, 0) }
	defer func() { now = orig }()

	l := New(3)
	for i := 0; i < 3; i++ {
		if !l.Allow("k1") {
			t.Fatalf("request %d denied, want allowed burst of 3", i+1)
		}
	}
	if l.Allow("k1") {
		t.Error("request 4 allowed, want denied (bucket empty)")
	}
	if !l.Allow("k2") {
		t.Error("independent key denied")
	}
}

func TestRefillsOverTime(t *testing.T) {
	current := time.Unix(1000, 0)
	now = func() time.Time { return current }
	defer func() { now = time.Now }()

	l := New(60) // refill rate: 1 per second
	for i := 0; i < 60; i++ {
		l.Allow("k")
	}
	if l.Allow("k") {
		t.Fatal("bucket should be empty")
	}

	current = current.Add(1 * time.Second) // refill rate: 1 token/second at 60 rpm
	if !l.Allow("k") {
		t.Error("request after refill denied")
	}
	if l.Allow("k") {
		t.Error("second request allowed — only one token refilled")
	}
}

func TestDisabledLimiter(t *testing.T) {
	l := New(0)
	for i := 0; i < 100; i++ {
		if !l.Allow("k") {
			t.Fatal("disabled limiter denied a request")
		}
	}
}
