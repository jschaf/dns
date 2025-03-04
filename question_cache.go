package dns

import (
	"net/netip"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// QuestionCache is a cache for DNS questions.
type QuestionCache interface {
	Get(q Question) (Answer, bool)
	Set(q Question, a Answer)
}

type Question struct {
	// Host is the fully qualified domain name with a trailing dot,
	// e.g. "example.com.".
	Host string
	// Type is the type of question. Must be either dnsmessage.TypeA or
	// dnsmessage.TypeAAAA.
	Type dnsmessage.Type
}

// Answer is the DNS answer for a Question. This is a simplified representation
// of dnsmessage.Message answers.
type Answer struct {
	// FetchTime is when the DNS record was requested.
	FetchTime time.Time
	// TTL is how long the answer is valid for.
	TTL time.Duration
	// IPs are the IP addresses for the DNS record.
	IPs []netip.Addr
}

type questionCache struct {
	m  map[dnsmessage.Question]Answer
	mu sync.RWMutex
}

func (c *questionCache) Get(q dnsmessage.Question) (Answer, bool) {
	c.mu.RLock()
	a, ok := c.m[q]
	c.mu.RUnlock()

	if !ok {
		return Answer{}, false
	}
	expireTime := a.FetchTime.Add(a.TTL)
	if expireTime.After(time.Now()) {
		c.mu.Lock()
		delete(c.m, q)
		c.mu.Unlock()
		return Answer{}, false
	}
}

func (c *questionCache) Set(q dnsmessage.Question, a Answer) {
	// Skip questions unrelated to A or AAAA records.
	if q.Class != dnsmessage.ClassINET {
		return
	}
	if q.Type != dnsmessage.TypeA && q.Type != dnsmessage.TypeAAAA {
		return
	}

	// Set the ExpireTime.
	var ttlSeconds uint32
	for _, r := range a.Message.Answers {
		if r.Header.Class == q.Class && r.Header.Type == q.Type && r.Header.TTL > 0 {
			ttlSeconds = r.Header.TTL
		}
	}
	// Skip this message if no TTL.
	if ttlSeconds == 0 {
		return
	}
	a.ExpireTime = a.FetchTime.Add(time.Duration(ttlSeconds) * time.Second)

	c.mu.Lock()
	c.m[q] = a
	c.mu.Unlock()
}
