package dns

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

var (
	_ net.Conn       = (*cacheConn)(nil)
	_ net.PacketConn = (*cacheConn)(nil)
)

// cacheConn is a read-through cache implementing net.Conn.
// Parses DNS requests, and returns cached DNS responses on cache hit.
// On a cache miss, delegates to a real conn and caches the results.
type cacheConn struct {
	// realConn is the real network connection. Initialized on the first (only)
	// write on a cache miss.
	realConn net.Conn
	// questionCache is the DNS cache.
	questionCache QuestionCache
	// dial creates realConn on a cache miss.
	dial func() (net.Conn, error)
	// cachedResp is the cached DNS response. Nil until the first write. Never set
	// on a cache miss.
	cachedResp *bytes.Reader
	// realResp is the DNS response from the real connection. Incrementally
	// written by Read calls and stored in the cache on Close.
	// Not used on a cache hit. Nil until the first Read.
	realResp []byte
}

func (c *cacheConn) Read(b []byte) (int, error) {
	if c.cachedResp != nil {
		return c.cachedResp.Read(b)
	}

	// Unreachable. We always set realConn on a cache miss.
	if c.realConn == nil {
		return 0, fmt.Errorf("read from conn on cache miss without a real connection")
	}

	// Cache miss. Read from the real connection and store the response so we can
	// cache it on Close.
	n, err := c.realConn.Read(b)
	if err != nil {
		return n, err
	}
	c.realResp = append(c.realResp, b[:n]...)
	return n, err
}

func (c *cacheConn) ReadFrom([]byte) (n int, addr net.Addr, err error) {
	return 0, nil, fmt.Errorf("cacheConn ReadFrom not implemented")
}

func (c *cacheConn) Write(b []byte) (n int, err error) {
	// Parse the DNS request to see if we have a cached answer.
	msg := &dnsmessage.Message{}
	if err := msg.Unpack(b); err != nil {
		return 0, fmt.Errorf("unpack dns message to check cache: %w", err)
	}

	// Only support a single question for simplicity.
	if len(msg.Questions) != 1 {
		c.realConn, err = c.dial()
		if err != nil {
			return 0, fmt.Errorf("dial conn for dns cache with multiple questions: %w", err)
		}
		return c.realConn.Write(b)
	}
	q := msg.Questions[0]

	// Only support A and AAAA records for simplicity.
	if q.Type != dnsmessage.TypeA && q.Type != dnsmessage.TypeAAAA {
		c.realConn, err = c.dial()
		if err != nil {
			return 0, fmt.Errorf("dial conn for dns cache with unsupported type %s: %w", q.Type, err)
		}
	}

	answer, ok := c.questionCache.Get(newQuestion(q))
	// Cache miss. Delegate to the real connection.
	if !ok {
		c.realConn, err = c.dial()
		if err != nil {
			return 0, fmt.Errorf("dial conn for dns cache on cache miss: %w", err)
		}
		return c.realConn.Write(b)
	}

	// Cache hit. Store the complete, packed DNS response for Read calls.
	// The Go implementation of dnsPacketRoundTrip uses a single Write call.
	msg.Answers, err = buildAnswers(q, answer)
	if err != nil {
		return 0, fmt.Errorf("build answers for dns cache on cache hit: %w", err)
	}
	msg.Response = true
	packed, err := msg.Pack()
	if err != nil {
		return 0, fmt.Errorf("pack dns message for dns cache on cache hit: %w", err)
	}
	c.cachedResp = bytes.NewReader(packed)

	return len(b), nil
}

func (c *cacheConn) WriteTo([]byte, net.Addr) (n int, err error) {
	return 0, fmt.Errorf("cacheConn WriteTo not implemented")
}

// buildAnswers returns the DNS answers for a question from the cached Answer.
func buildAnswers(q dnsmessage.Question, answer Answer) ([]dnsmessage.Resource, error) {
	answers := make([]dnsmessage.Resource, 0, len(answer.IPs))
	for _, ip := range answer.IPs {
		// The cached IP address must match the requested type since the cache key,
		// a Question, includes the type.
		if q.Type == dnsmessage.TypeA && !ip.Is4() {
			return nil, fmt.Errorf("cached IP address %s is not the correct type for dns A record for host %s", ip, q.Name.String())
		}
		if q.Type == dnsmessage.TypeAAAA && !ip.Is6() {
			return nil, fmt.Errorf("cached IP address %s is not the correct type for dns AAAA record for host %s", ip, q.Name.String())
		}

		resource := dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{
				Name:  q.Name,
				Type:  q.Type,
				Class: q.Class,
				TTL:   uint32(answer.TTL / time.Second), //nolint:gosec
			},
		}
		switch {
		case q.Type == dnsmessage.TypeA:
			resource.Body = &dnsmessage.AResource{A: ip.As4()}
		case q.Type == dnsmessage.TypeAAAA:
			resource.Body = &dnsmessage.AAAAResource{AAAA: ip.As16()}
		default:
			return nil, fmt.Errorf("unsupported record type: %v", q.Type)
		}
		answers = append(answers, resource)
	}

	return answers, nil
}

func (c *cacheConn) Close() (mErr error) {
	// Cache hit, nothing to do.
	if c.realConn == nil {
		return nil
	}

	// Always close the conn.
	defer capture(&mErr, c.realConn.Close, "close real conn")

	// Cache miss, but we didn't get response. Network error?
	if len(c.realResp) == 0 {
		return nil
	}

	// Cache miss. Store the response in the cache.
	msg := &dnsmessage.Message{}
	if err := msg.Unpack(c.realResp); err != nil {
		return fmt.Errorf("unpack response to cache on close: %w", err)
	}

	// Only support a single question for simplicity.
	if len(msg.Questions) != 1 {
		return nil
	}

	// Store the response in the cache.
	question := newQuestion(msg.Questions[0])
	answer, err := newAnswer(msg)
	if err != nil {
		return fmt.Errorf("build new answer to cache on close: %w", err)
	}
	if !answer.IsExpired() {
		c.questionCache.Set(question, answer)
	}

	return nil
}

func (c *cacheConn) LocalAddr() net.Addr {
	if c.realConn == nil {
		return nil
	}
	return c.realConn.LocalAddr()
}

func (c *cacheConn) RemoteAddr() net.Addr {
	if c.realConn == nil {
		return nil
	}
	return c.realConn.RemoteAddr()
}

func (c *cacheConn) SetDeadline(t time.Time) error {
	if c.realConn == nil {
		return nil
	}
	return c.realConn.SetDeadline(t)
}

func (c *cacheConn) SetReadDeadline(t time.Time) error {
	if c.realConn == nil {
		return nil
	}
	return c.realConn.SetReadDeadline(t)
}

func (c *cacheConn) SetWriteDeadline(t time.Time) error {
	if c.realConn == nil {
		return nil
	}
	return c.realConn.SetWriteDeadline(t)
}

// capture runs errFunc and assigns the error, if any, to *errPtr.
// Preserves the original error by wrapping with errors.Join if
// errFunc returns a non-nil error.
func capture(errPtr *error, errFunc func() error, msg string) {
	err := errFunc()
	if err == nil {
		return
	}
	*errPtr = errors.Join(*errPtr, fmt.Errorf("%s: %w", msg, err))
}
