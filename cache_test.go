package dns

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	host     = "test-cache.example.com"
	wantBody = "ok - test cache"
)

func TestCache(t *testing.T) {
	ctx := t.Context()

	// We need a test http server that the resolver will resolve host to the
	// local httpServer addr.
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(wantBody))
	}))
	defer httpServer.Close()
	httpAddr := httpServer.Listener.Addr().String()
	httpHost, httpPort, err := net.SplitHostPort(httpAddr)
	if err != nil {
		t.Fatalf("net.SplitHostPort: %v", err)
	}
	httpIPAddr, err := netip.ParseAddr(httpHost)
	if err != nil {
		t.Fatalf("netip.ParseAddr: %v", err)
	}
	t.Logf("http test server: %s:%s", httpHost, httpPort)

	var dnsDoneInfo httptrace.DNSDoneInfo
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			t.Logf("GotConn: %v", info.Conn.RemoteAddr().String())
		},
		DNSStart: func(info httptrace.DNSStartInfo) {
			t.Logf("DNS Start: host=%s", info.Host)
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			t.Logf("DNSDone: addrs=%v", info.Addrs)
			dnsDoneInfo = info
		},
		ConnectStart: func(network, addr string) {
			t.Logf("ConnectStart: network=%s, addr=%s", network, addr)
		},
		ConnectDone: func(network, addr string, err error) {
			t.Logf("ConnectDone:  network=%s, addr=%s, err=%v", network, addr, err)
		},
	})

	cache := &Cache{
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			t.Logf("dial fake dns server: %s", network)
			fakeDNS := newFakeDNSARecord(host, httpIPAddr)
			return fakeDNS.DialContext(ctx, network, addr)
		},
	}
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:  1 * time.Second,
			Resolver: cache.Resolver(),
		}).DialContext,
	}
	client := &http.Client{
		Transport: transport,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+":"+httpPort, nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("client.Get: expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}
	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if string(gotBody) != wantBody {
		t.Fatalf("expected body %q, got %q", wantBody, gotBody)
	}
	if dnsDoneInfo.Err != nil {
		t.Fatalf("DNSDoneInfo.Err: %v", dnsDoneInfo.Err)
	}
	if len(dnsDoneInfo.Addrs) == 0 {
		t.Fatalf("DNSDoneInfo.Addrs empty: want %v", httpIPAddr.String())
	}
	if len(dnsDoneInfo.Addrs) != 1 && dnsDoneInfo.Addrs[0].String() != httpIPAddr.String() {
		t.Fatalf("DNSDoneInfo.Addrs.len: want %v, got %v", httpIPAddr.String(), dnsDoneInfo.Addrs)
	}
}

// newFakeDNSARecord returns a fake DNS server that responds to A record queries
// for host with the given IP address.
func newFakeDNSARecord(host string, ip netip.Addr) *fakeDNSServer {
	fakeDNS := &fakeDNSServer{
		rh: func(n, _ string, q dnsmessage.Message, _ time.Time) (dnsmessage.Message, error) {
			r := dnsmessage.Message{
				Header: dnsmessage.Header{
					ID:       q.Header.ID,
					Response: true,
					RCode:    dnsmessage.RCodeSuccess,
				},
				Questions: q.Questions,
			}
			fqdn := dnsmessage.MustNewName(host + ".")
			if len(q.Questions) == 1 &&
				q.Questions[0].Type == dnsmessage.TypeA &&
				q.Questions[0].Class == dnsmessage.ClassINET &&
				fqdn == q.Questions[0].Name {
				r.Answers = []dnsmessage.Resource{
					{
						Header: dnsmessage.ResourceHeader{
							Name:   q.Questions[0].Name,
							Type:   dnsmessage.TypeA,
							Class:  dnsmessage.ClassINET,
							Length: 4,
						},
						Body: &dnsmessage.AResource{
							A: ip.As4(),
						},
					},
				}
			}

			return r, nil
		},
	}
	return fakeDNS
}

type fakeDNSServer struct {
	rh        func(n, s string, q dnsmessage.Message, t time.Time) (dnsmessage.Message, error)
	alwaysTCP bool
}

func (server *fakeDNSServer) DialContext(_ context.Context, n, s string) (net.Conn, error) {
	if server.alwaysTCP || n == "tcp" || n == "tcp4" || n == "tcp6" {
		return &fakeDNSConn{tcp: true, server: server, n: n, s: s}, nil
	}
	return &fakeDNSPacketConn{fakeDNSConn: fakeDNSConn{tcp: false, server: server, n: n, s: s}}, nil
}

type fakeDNSConn struct {
	net.Conn
	tcp    bool
	server *fakeDNSServer
	n      string
	s      string
	q      dnsmessage.Message
	t      time.Time
	buf    []byte
}

func (f *fakeDNSConn) Close() error {
	return nil
}

func (f *fakeDNSConn) Read(b []byte) (int, error) {
	if len(f.buf) > 0 {
		n := copy(b, f.buf)
		f.buf = f.buf[n:]
		return n, nil
	}

	resp, err := f.server.rh(f.n, f.s, f.q, f.t)
	if err != nil {
		return 0, err
	}

	bb := make([]byte, 2, 514)
	bb, err = resp.AppendPack(bb)
	if err != nil {
		return 0, fmt.Errorf("cannot marshal DNS message: %w", err)
	}

	if f.tcp {
		l := len(bb) - 2
		bb[0] = byte(l >> 8)
		bb[1] = byte(l)
		f.buf = bb
		return f.Read(b)
	}

	bb = bb[2:]
	if len(b) < len(bb) {
		return 0, errors.New("read would fragment DNS message")
	}

	copy(b, bb)
	return len(bb), nil
}

func (f *fakeDNSConn) Write(b []byte) (int, error) {
	if f.tcp && len(b) >= 2 {
		b = b[2:]
	}
	if f.q.Unpack(b) != nil {
		return 0, fmt.Errorf("cannot unmarshal DNS message fake %s (%d)", f.n, len(b))
	}
	return len(b), nil
}

func (f *fakeDNSConn) SetDeadline(t time.Time) error {
	f.t = t
	return nil
}

type fakeDNSPacketConn struct {
	net.PacketConn
	fakeDNSConn
}

func (f *fakeDNSPacketConn) SetDeadline(t time.Time) error {
	return f.fakeDNSConn.SetDeadline(t)
}

func (f *fakeDNSPacketConn) Close() error {
	return f.fakeDNSConn.Close()
}
