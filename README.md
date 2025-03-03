# dns - caching DNS resolver for Go HTTP clients

The Go HTTP client does not cache DNS lookups. This means every outbound request
incurs a DNS lookup. Usually, the host operating system caches DNS lookups.
However, for small containers, like [distroless], the host OS doesn't cache DNS
lookups.

[distroless]: http://github.com/GoogleContainerTools/distroless

# Get started

Create a new `http.Client` with a `dns.Resolver` as the `http.Transport`'s
`DialContext` function. The `DialContext` function is called for every new
outbound connection.

```go
package main

import (
	"net/http"

	"github.com/jschaf/dns"
)

var cachingClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Resolver: dns.NewCacheResolver(),
		}).DialContext,
	},
}

// To replace the global, default HTTP client, use the `http.DefaultClient`
// variable.
func init() {
    http.DefaultClient = cachingClient
}

```

# Pitch

Why should you use `dns.Resolver`?

- Increase performance of HTTP clients, reducing unnecessary round trips
  for DNS lookups.

- Avoid external dependencies, like separate a caching DNS resolver.

# Anti-pitch

I'd like to try to convince you why you shouldn't use pggen. Often, this is
more revealing than the pitch.

- Your host OS already caches DNS lookups. In this case, the extra complexity
  of one of the two hard problems in computer science (caching) is not worth it.

- You have enough operational expertise to run a caching DNS resolver somewhere
  in your stack. In Kubernetes, this is probably [NodeLocal DNSCache].

- A standalone caching DNS resolver reduces the total number of DNS lookups
  across all clients, not just HTTP clients. This is a more efficient use of
  resources.

[NodeLocal DNSCache]: https://kubernetes.io/docs/tasks/administer-cluster/nodelocaldns/
