package dns

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestCache(t *testing.T) {
	ctx, getResolvedAddrs := captureResolvedAddrs(t, t.Context())
	fakeHTTP, fakeDNS := startServers(t, "test-cache.example.com")

	cache := &Cache{
		Dial: fakeDNS.DialContext,
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:  1 * time.Second,
				Resolver: cache.Resolver(),
			}).DialContext,
		},
	}

	err := doGetRequest(ctx, client, fakeHTTP.URI)
	if err != nil {
		t.Fatalf("doGetRequest: %v", err)
	}

	want := []netip.Addr{fakeHTTP.IP}
	got := getResolvedAddrs()
	assertSameAddrs(t, want, got)

	// Second request should use the cache.
	// Error out on the DNS request to ensure it's not called.
	fakeDNS.handler = func(network string, q dnsmessage.Message) (dnsmessage.Message, error) {
		return dnsmessage.Message{}, fmt.Errorf("should not be called")
	}
	err = doGetRequest(ctx, client, fakeHTTP.URI)
	if err != nil {
		t.Fatalf("doGetRequest: %v", err)
	}
	got = getResolvedAddrs()
	assertSameAddrs(t, want, got)
}
