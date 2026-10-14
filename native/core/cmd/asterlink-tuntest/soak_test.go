package main

import (
	"testing"
	"time"
)

func series(n int, f func(i int) soakSample) []soakSample {
	out := make([]soakSample, n)
	for i := range out {
		out[i] = f(i)
		out[i].At = time.Duration(i) * time.Minute
	}
	return out
}

func TestJudgeSoak(t *testing.T) {
	steady := func(i int) soakSample { return soakSample{Goroutines: 40, Threads: 12, Handles: 300, HeapMiB: 20} }
	if ok, d := judgeSoak(series(10, steady)); !ok {
		t.Fatalf("a steady series failed: %s", d)
	}
	if ok, _ := judgeSoak(series(3, steady)); ok {
		t.Fatal("too few samples must not pass")
	}
	cases := map[string]func(i int) soakSample{
		"handle leak": func(i int) soakSample {
			return soakSample{Goroutines: 40, Threads: 12, Handles: 300 + 20*i, HeapMiB: 20}
		},
		"goroutine leak": func(i int) soakSample {
			return soakSample{Goroutines: 40 + 20*i, Threads: 12, Handles: 300, HeapMiB: 20}
		},
		"heap growth": func(i int) soakSample {
			return soakSample{Goroutines: 40, Threads: 12, Handles: 300, HeapMiB: 20 + 30*float64(i)}
		},
		"thread growth": func(i int) soakSample {
			return soakSample{Goroutines: 40, Threads: 12 + 10*i, Handles: 300, HeapMiB: 20}
		},
		"exit left the tunnel": func(i int) soakSample {
			return soakSample{Goroutines: 40, Threads: 12, Handles: 300, HeapMiB: 20, Probed: i == 6, ExitOK: false}
		},
	}
	for name, f := range cases {
		if ok, d := judgeSoak(series(10, f)); ok {
			t.Errorf("%s was not detected: %s", name, d)
		}
	}
	warm := func(i int) soakSample {
		s := steady(i)
		if i < 2 {
			s.Handles, s.Goroutines = 100, 10 // start-up: growth inside the warm-up is expected
		}
		return s
	}
	if ok, d := judgeSoak(series(10, warm)); !ok {
		t.Fatalf("warm-up growth must be ignored: %s", d)
	}
}
