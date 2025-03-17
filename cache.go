package dns

import (
	"context"
	"net"
	"strings"
	"sync"
)

// Cache is a DNS cache that uses net.Resolver for an http.Transport.
// Typically used to cache DNS queries as part of an http.Client.
//
// As a minimal example:
//
//	dnsCache := &dns.Cache{}
//	client := &http.Client{
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

	// QuestionCache is an optional cache for DNS questions.
	//
	// If nil, Cache uses a simple in-memory cache.
	QuestionCache QuestionCache

	initOnce sync.Once
	resolver *net.Resolver
}

func (c *Cache) init() {
	c.initOnce.Do(func() {
		defaultDialer := &net.Dialer{}
		if c.Dial == nil {
			c.Dial = defaultDialer.DialContext
		}
		if c.QuestionCache == nil {
			c.QuestionCache = newQuestionCache()
		}
		c.resolver = &net.Resolver{
			StrictErrors: true,
			PreferGo:     true,
			Dial:         c.dial,
		}
	})
}

func (c *Cache) Resolver() *net.Resolver {
	c.init()
	return c.resolver
}

func (c *Cache) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	// Only support UDP for simplicity. TCP prepends a 2-byte length prefix.
	// The Go resolver tests whether the conn implements net.PacketConn rather
	// than testing the network string for reads, so TCP support might also need
	// a new, separate conn type that doesn't implement net.PacketConn.
	// Test a prefix because udp4 and upd6 are valid network strings.
	if !strings.HasPrefix(network, "udp") {
		return c.Dial(ctx, network, addr)
	}
	conn := &cacheConn{
		questionCache: c.QuestionCache,
		dial:          func() (net.Conn, error) { return c.Dial(ctx, network, addr) },
	}
	return conn, nil
}
