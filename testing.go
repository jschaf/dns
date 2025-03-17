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
	"slices"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const wantBody = "ok - test cache"

type httpServer struct {
	IP   netip.Addr
	Port string
	// FQDN is the fully qualified domain name of the test server.
	FQDN string
	URI  string
}

// startServers starts a test HTTP server and a fake DNS server. Adds a
// DNS A record for host with the IP address of the test HTTP server.
func startServers(t *testing.T, host string) (httpServer, *dnsServer) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(wantBody))
	}))
	t.Cleanup(server.Close)
	httpHost, httpPort, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("net.SplitHostPort: %v", err)
	}
	ip, err := netip.ParseAddr(httpHost)
	if err != nil {
		t.Fatalf("netip.ParseAddr: %v", err)
	}

	h := httpServer{
		IP:   ip,
		Port: httpPort,
		FQDN: host + ".",
		URI:  fmt.Sprintf("http://%s:%s", host, httpPort),
	}
	d := startDNSServer(t, h.FQDN, h.IP)
	return h, d
}

// doGetRequest sends a GET request to the test server and checks for a
// successful response.
func doGetRequest(ctx context.Context, client *http.Client, uri string) (mErr error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return fmt.Errorf("build get request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}
	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	defer capture(&mErr, resp.Body.Close, "close response body")
	if string(gotBody) != wantBody {
		return fmt.Errorf("got body %q, want %q", string(gotBody), wantBody)
	}
	return nil
}

// captureResolvedAddrs returns a new context that captures the resolved IP
// addresses using httptrace.ClientTrace.
func captureResolvedAddrs(t *testing.T, ctx context.Context) (context.Context, func() []netip.Addr) {
	t.Helper()
	var dnsDoneInfo httptrace.DNSDoneInfo
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			t.Logf("got_conn: remote_addr=%v reused=%v", info.Conn.RemoteAddr().String(), info.Reused)
		},
		DNSStart: func(info httptrace.DNSStartInfo) {
			t.Logf("dns_start: host=%s", info.Host)
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			t.Logf("dns_done: addrs=%v", info.Addrs)
			dnsDoneInfo = info
		},
		ConnectStart: func(network, addr string) {
			t.Logf("connect_start: network=%s, addr=%s", network, addr)
		},
		ConnectDone: func(network, addr string, err error) {
			t.Logf("connect_done:  network=%s, addr=%s, err=%v", network, addr, err)
		},
	})
	return ctx, func() []netip.Addr {
		if dnsDoneInfo.Err != nil {
			t.Fatalf("DNSDoneInfo.Err: %v", dnsDoneInfo.Err)
		}
		ips := make([]netip.Addr, 0, len(dnsDoneInfo.Addrs))
		for _, a := range dnsDoneInfo.Addrs {
			ip, err := netip.ParseAddr(a.String())
			if err != nil {
				t.Fatalf("netip.ParseAddr: %v", err)
			}
			ips = append(ips, ip)
		}
		return ips
	}
}

// startDNSServer returns a fake DNS server that responds to A record queries
// for host with the given IP address.
func startDNSServer(t *testing.T, fqdnHost string, ip netip.Addr) *dnsServer {
	fakeDNS := &dnsServer{
		t: t,
		handler: func(network string, q dnsmessage.Message) (dnsmessage.Message, error) {
			r := dnsmessage.Message{
				Header: dnsmessage.Header{
					ID:       q.Header.ID,
					Response: true,
					RCode:    dnsmessage.RCodeSuccess,
				},
				Questions: q.Questions,
			}
			fqdn := dnsmessage.MustNewName(fqdnHost)
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

type dnsServer struct {
	t       *testing.T
	handler func(network string, q dnsmessage.Message) (dnsmessage.Message, error)
}

func (s *dnsServer) DialContext(_ context.Context, network, _ string) (net.Conn, error) {
	s.t.Logf("dial fake dns server network %s", network) // addr is ignored
	return &fakeDNSConn{server: s, network: network}, nil
}

type fakeDNSConn struct {
	net.Conn
	tcp      bool
	server   *dnsServer
	network  string
	question dnsmessage.Message
	buf      []byte
}

func (f *fakeDNSConn) Read(b []byte) (int, error) {
	if len(f.buf) > 0 {
		n := copy(b, f.buf)
		f.buf = f.buf[n:]
		return n, nil
	}

	resp, err := f.server.handler(f.network, f.question)
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
	if f.question.Unpack(b) != nil {
		return 0, fmt.Errorf("cannot unmarshal DNS message fake %s (%d)", f.network, len(b))
	}
	return len(b), nil
}

func (f *fakeDNSConn) Close() error                { return nil }
func (f *fakeDNSConn) SetDeadline(time.Time) error { return nil }

func cmpNetIP(a, b netip.Addr) int { return a.Compare(b) }

// assertSameAddrs checks that want and got contain the same IP addresses.
func assertSameAddrs(t *testing.T, want []netip.Addr, got []netip.Addr) {
	slices.SortFunc(want, cmpNetIP)
	slices.SortFunc(got, cmpNetIP)
	if len(want) != len(got) {
		t.Fatalf("want %d addresses, got %d\nwant: %v\ngot:  %v", len(want), len(got), want, got)
	}
}
