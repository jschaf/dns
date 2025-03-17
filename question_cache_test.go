package dns

import (
	"net/netip"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestQuestionCache_Get(t *testing.T) {
	qc := newQuestionCache()

	q1 := Question{FQDN: "example.com.", Type: dnsmessage.TypeA}
	a1 := Answer{
		FetchTime: time.Now(),
		TTL:       20 * time.Millisecond,
		IPs:       []netip.Addr{netip.MustParseAddr("1.2.3.4")},
	}

	// No cache entry.
	if got, ok := qc.Get(q1); ok {
		t.Errorf("empty question: got = %v; want false", got)
	}
	assertHitsMisses(t, qc, 0, 1)

	// Set and get cache entry.
	qc.Set(q1, a1)
	if got, ok := qc.Get(q1); !ok {
		t.Errorf("set question: got = %v, %v; want %v, true", got, ok, a1)
	} else {
		assertSameAnswer(t, a1, got)
	}
	assertHitsMisses(t, qc, 1, 1)

	// Expired cache entry.
	time.Sleep(a1.TTL)
	if got, ok := qc.Get(q1); ok {
		t.Errorf("expired question: got = %v; want false", got)
	}
	assertHitsMisses(t, qc, 1, 2)
}

const (
	goroutineCount = 8
	runCount       = 256
)

func TestQuestionCache_StressGetMissing(t *testing.T) {
	qc := newQuestionCache()

	q1 := Question{FQDN: "example.com.", Type: dnsmessage.TypeA}

	runParallel(func(int) {
		got, ok := qc.Get(q1)
		if ok {
			t.Fatalf("want missing answer; got %v", got)
		}
	})

	assertHitsMisses(t, qc, 0, int64(goroutineCount*runCount))
}

func TestQuestionCache_StressGetPresent(t *testing.T) {
	qc := newQuestionCache()

	q1 := Question{FQDN: "example.com.", Type: dnsmessage.TypeA}
	a1 := Answer{
		FetchTime: time.Now(),
		TTL:       20 * time.Millisecond,
		IPs:       []netip.Addr{netip.MustParseAddr("1.2.3.4")},
	}
	qc.Set(q1, a1)

	runParallel(func(int) {
		_, ok := qc.Get(q1)
		if !ok {
			t.Fatal("want present answer; got missing")
		}
	})

	assertHitsMisses(t, qc, int64(goroutineCount*runCount), 0)
}

func TestQuestionCache_StressGetSet(t *testing.T) {
	qc := newQuestionCache()

	q1 := Question{FQDN: "example.com.", Type: dnsmessage.TypeA}
	a1 := Answer{
		FetchTime: time.Now(),
		TTL:       20 * time.Millisecond,
		IPs:       []netip.Addr{netip.MustParseAddr("1.2.3.4")},
	}

	runParallel(func(i int) {
		if i%4 == 0 {
			qc.Set(q1, a1)
		}
		_, ok := qc.Get(q1)
		if !ok {
			t.Fatal("want present answer; got missing")
		}
	})

	assertHitsMisses(t, qc, int64(goroutineCount*runCount), 0)
}

func runParallel(f func(i int)) {
	var wg sync.WaitGroup
	for range goroutineCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range runCount {
				f(i)
			}
		}()
	}
	wg.Wait()
}

func assertSameAnswer(t *testing.T, want, got Answer) {
	t.Helper()
	wantStr := want.GoString()
	gotStr := got.GoString()
	if wantStr != gotStr {
		t.Errorf("answer mismatch\nwant: %s\ngot:  %s", wantStr, gotStr)
	}
}

func assertHitsMisses(t *testing.T, qc *questionCache, wantHits, wantMisses int64) {
	t.Helper()
	if got := qc.hits.Load(); got != wantHits {
		t.Errorf("hits: want %d, got %d", got, wantHits)
	}
	if got := qc.misses.Load(); got != wantMisses {
		t.Errorf("misses: got = %d; want %d", got, wantMisses)
	}
}
