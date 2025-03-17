package dns

import (
	"net/netip"
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
