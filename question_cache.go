package dns

import (
	"fmt"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// QuestionCache is a cache for DNS questions.
type QuestionCache interface {
	Get(q Question) (Answer, bool)
	Set(q Question, a Answer)
}

// Question is a DNS question. This is a simplified representation of
// dnsmessage.Question.
type Question struct {
	// FQDN is the fully qualified domain name with a trailing dot,
	// e.g. "example.com.".
	FQDN string
	// Type is the type of question. Must be either dnsmessage.TypeA or
	// dnsmessage.TypeAAAA.
	Type dnsmessage.Type
}

// newQuestion creates a new Question from a dnsmessage.Question.
func newQuestion(q dnsmessage.Question) Question {
	return Question{FQDN: q.Name.String(), Type: q.Type}
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

func newAnswer(m *dnsmessage.Message) (Answer, error) {
	if len(m.Answers) == 0 {
		return Answer{}, fmt.Errorf("no answers in DNS message")
	}
	a := Answer{
		FetchTime: time.Now(),
		TTL:       time.Duration(m.Answers[0].Header.TTL) * time.Second,
		IPs:       make([]netip.Addr, 0, len(m.Answers)),
	}
	for _, r := range m.Answers {
		//nolint:exhaustive
		switch r.Header.Type {
		case dnsmessage.TypeA:
			res, ok := r.Body.(*dnsmessage.AResource)
			if !ok {
				return Answer{}, fmt.Errorf("invalid A record body: %v", r.Body)
			}
			a.IPs = append(a.IPs, netip.AddrFrom4(res.A))
		case dnsmessage.TypeAAAA:
			res, ok := r.Body.(*dnsmessage.AAAAResource)
			if !ok {
				return Answer{}, fmt.Errorf("invalid AAAA record body: %v", r.Body)
			}
			a.IPs = append(a.IPs, netip.AddrFrom16(res.AAAA))
		default:
			return Answer{}, fmt.Errorf("unsupported record type: %v", r.Header.Type)
		}
	}
	return a, nil
}

func (a Answer) IsExpired() bool {
	return a.FetchTime.Add(a.TTL).Before(time.Now())
}

func (a Answer) GoString() string {
	return fmt.Sprintf("Answer{FetchTime: %s, TTL: %ds, IPs: %v}", a.FetchTime.Format(time.DateTime), int(a.TTL.Seconds()), a.IPs)
}

var _ QuestionCache = &questionCache{}

type questionCache struct {
	m      map[Question]Answer
	mu     sync.RWMutex
	hits   *atomic.Int64
	misses *atomic.Int64
}

func newQuestionCache() *questionCache {
	return &questionCache{
		m:      make(map[Question]Answer),
		hits:   new(atomic.Int64),
		misses: new(atomic.Int64),
	}
}

func (c *questionCache) Get(q Question) (Answer, bool) {
	c.mu.RLock()
	a, ok := c.m[q]
	c.mu.RUnlock()

	if !ok {
		c.misses.Add(1)
		return Answer{}, false
	}

	if a.IsExpired() {
		c.mu.Lock()
		delete(c.m, q)
		c.mu.Unlock()
		c.misses.Add(1)
		return Answer{}, false
	}

	c.hits.Add(1)
	return a, true
}

func (c *questionCache) Set(q Question, a Answer) {
	c.mu.Lock()
	c.m[q] = a
	c.mu.Unlock()
}
