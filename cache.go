package dns

import (
	"context"
	"net"
	"sync"
)

type CacheResolverOpts struct {
	// Dialer is the dialer to use for DNS queries.
	// If unset, uses a default dialer.
	Dialer *net.Dialer
}

// Cache is a DNS cache that uses net.Resolver for an http.Transport.
// Typically used for to cache DNS queries as part of an http.Client.
//
// As a minimal example:
//
//	var dnsCache = &dns.Cache{}
//	var client = &http.Client{
//		Transport: &http.Transport{
//			DialContext: (&net.Dialer{
//				Resolver: dnsCache.Resolver(),
//			}).DialContext,
//		},
//	}
type Cache struct {
	// Dial optionally specifies an alternate dialer for use by Go's built-in
	// DNS resolver to make TCP and UDP connections to DNS services. The host
	// in the address parameter will always be a literal IP address and not a
	// host name, and the port in the address parameter will be a literal port
	// number and not a service name.
	//
	// If the Conn returned is also a PacketConn, sent and received DNS messages
	// must adhere to RFC 1035 section 4.2.1, "UDP usage". Otherwise, DNS
	// messages transmitted over Conn must adhere to RFC 7766 section 5,
	// "Transport Protocol Selection".
	//
	// If nil, the default dialer is used.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)

	once     sync.Once // init resolver
	resolver *net.Resolver
}

func (c *Cache) Resolver() *net.Resolver {
	c.once.Do(func() {
		if c.Dial == nil {
			d := &net.Dialer{}
			c.Dial = d.DialContext
		}
		c.resolver = &net.Resolver{
			StrictErrors: true,
			PreferGo:     true,
			Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
				// TODO: implement DNS caching
				return c.Dial(ctx, network, addr)
			},
		}
	})
	return c.resolver
}
