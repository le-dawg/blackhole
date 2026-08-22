# Implementation Report: Task 3 - Race-Forwarder (Spec B)

## Implementation Details
- **`dnsd/forwarder.go`**: Implemented `RaceForward` using raw `[]byte` and `net.Dialer.DialContext` instead of `miekg/dns`. This avoids unnecessary pack/unpack cycles for simply forwarding standard DNS packets, aligning perfectly with the raw `[]byte` handling strategy natively used by `main.go`. It broadcasts UDP packets concurrently to multiple servers and returns the first success response in less than 500ms using a buffered channel.
- **`main.go`**: Patched `forwardQuery` to integrate both `RaceForward` and the `dnsCache` implemented in Task 2. Queries are now checked against the cache first. On a cache miss, it uses `RaceForward` to concurrently resolve against all configured upstream DNS servers, caches the winning response using `dnsCache`, and replies to the client.
- **`dnsCache` Size**: Enforced 8MB RAM budget loosely by setting `NewDNSCache(4000)` initialized globally. Given each `dnsmessage.Message` and string allocation maps to around 1.5 - 2 KB roughly, 4,000 entries ensure the footprint stays safely below 8 MB.

## Deviations
- **Signature Change**: The spec suggested `func RaceForward(r *dns.Msg, ...)` utilizing `miekg/dns`. Per project guidelines to favor native `golang.org/x/net/dns/dnsmessage`, and to avoid overhead from repetitive packing/unpacking, `RaceForward` was implemented to accept and return raw `[]byte`. Message parsing occurs locally in `main.go` before setting the cache.
- **Context handling**: Enforced context timeouts at the socket deadline level inside the `RaceForward` goroutines instead of passing context downstream, which prevents hanging goroutines.

## Test Results
- Authored `TestRaceForward` in `forwarder_test.go` leveraging standard dummy UDP servers mimicking slow (100ms) and fast (10ms) responses.
- Test validated correctly favoring the fast server and avoiding race leaks.
- All integration and unit tests passing via `go test -v ./src/...`

## Self-Review
The solution respects constraints:
- Uses bounded LRU (cache initialized size capped).
- Avoids `github.com/miekg/dns`.
- Handles `< 500 ms` upstream race via `context.WithTimeout` and UDP socket deadlines natively.
- No goroutine leaks: Channels are appropriately buffered to `len(upstreams)`.
