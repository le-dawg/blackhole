package dnsd

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func validateDNSResponse(reqRaw, respRaw []byte) error {
	var req, resp dnsmessage.Message
	if err := req.Unpack(reqRaw); err != nil {
		return err
	}
	if err := resp.Unpack(respRaw); err != nil {
		return err
	}

	if !resp.Header.Response {
		return errors.New("upstream returned non-response message")
	}

	if req.Header.ID != resp.Header.ID {
		return errors.New("txid mismatch")
	}

	if resp.Header.OpCode != req.Header.OpCode {
		return errors.New("opcode mismatch")
	}

	if len(req.Questions) != 1 || len(resp.Questions) != 1 {
		return errors.New("invalid question cardinality")
	}

	q := req.Questions[0]
	rq := resp.Questions[0]
	if q.Name != rq.Name || q.Type != rq.Type || q.Class != rq.Class {
		return errors.New("qname/qtype/qclass mismatch")
	}

	if len(resp.Answers)+len(resp.Authorities)+len(resp.Additionals) > 100 {
		return errors.New("excessive resource records")
	}

	qNameStr := strings.ToLower(q.Name.String())

	// Build exact CNAME graph traversal from qNameStr
	validNames := make(map[string]bool)
	validNames[qNameStr] = true

	cnameMap := make(map[string]string)
	for _, ans := range resp.Answers {
		if cname, ok := ans.Body.(*dnsmessage.CNAMEResource); ok {
			cnameMap[strings.ToLower(ans.Header.Name.String())] = strings.ToLower(cname.CNAME.String())
		}
	}

	curr := qNameStr
	visited := make(map[string]bool)
	for hop := 0; hop < 8; hop++ {
		visited[curr] = true
		next, hasNext := cnameMap[curr]
		if !hasNext {
			break
		}
		if visited[next] {
			return errors.New("cname loop detected")
		}
		validNames[next] = true
		curr = next
	}

	// Validate Answer records are on the CNAME graph path
	for _, ans := range resp.Answers {
		ansName := strings.ToLower(ans.Header.Name.String())
		if !validNames[ansName] {
			return errors.New("bailiwick mismatch: answer record not in query cname path")
		}
	}

	// Validate Authority records are within zone bailiwick
	authorityNS := make(map[string]bool)
	for _, auth := range resp.Authorities {
		authName := strings.ToLower(auth.Header.Name.String())
		if !isZoneBailiwick(qNameStr, authName) {
			return errors.New("bailiwick mismatch: authority record out of zone")
		}
		if ns, ok := auth.Body.(*dnsmessage.NSResource); ok {
			authorityNS[strings.ToLower(ns.NS.String())] = true
		}
	}

	// Validate Additional records (except OPT) match CNAME path or authoritative NS glue
	for _, add := range resp.Additionals {
		if add.Header.Type == dnsmessage.TypeOPT {
			continue // EDNS0 OPT record is allowed
		}
		addName := strings.ToLower(add.Header.Name.String())
		if !validNames[addName] && !authorityNS[addName] {
			return errors.New("bailiwick mismatch: untrusted additional record")
		}
	}

	return nil
}

func isZoneBailiwick(qname, zone string) bool {
	qname = strings.TrimSuffix(strings.ToLower(qname), ".")
	zone = strings.TrimSuffix(strings.ToLower(zone), ".")
	if qname == zone {
		return true
	}
	return strings.HasSuffix(qname, "."+zone)
}

func RaceForward(rawMsg []byte, upstreams []string, timeout time.Duration, dialContext func(ctx context.Context, network, addr string) (net.Conn, error)) ([]byte, error) {
	if dialContext == nil {
		dialContext = (&net.Dialer{}).DialContext
	}
	if len(upstreams) == 0 {
		return nil, errors.New("no upstreams configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resultCh := make(chan []byte, len(upstreams))
	var wg sync.WaitGroup

	for _, upstream := range upstreams {
		wg.Add(1)
		go func(server string) {
			defer wg.Done()
			conn, err := dialContext(ctx, "udp", server)
			if err != nil {
				return
			}
			defer conn.Close()

			// Set the deadline to match the context timeout
			if deadline, ok := ctx.Deadline(); ok {
				_ = conn.SetDeadline(deadline)
			}

			if _, err := conn.Write(rawMsg); err != nil {
				return
			}

			respBuf := make([]byte, 4096)
			n, err := conn.Read(respBuf)
			if err == nil && n > 0 {
				if err := validateDNSResponse(rawMsg, respBuf[:n]); err == nil {
					select {
					case resultCh <- respBuf[:n]:
					default:
					}
				}
			}
		}(upstream)
	}

	allFailed := make(chan struct{})
	go func() {
		wg.Wait()
		close(allFailed)
	}()

	select {
	case resp := <-resultCh:
		return resp, nil
	case <-allFailed:
		select {
		case resp := <-resultCh:
			return resp, nil
		default:
			return nil, errors.New("all upstreams failed")
		}
	case <-ctx.Done():
		return nil, errors.New("upstream timeout")
	}
}

// Filter defines an interface for filtering DNS requests.
type Filter interface {
	Process(req []byte) (resp []byte, block bool, err error)
}

// FilterChain is a chain of filters to be evaluated sequentially.
type FilterChain []Filter

var (
	globalFilters []Filter
	filtersMu     sync.RWMutex
)

// RegisterFilter allows third-party extensions to register their filters dynamically.
// This is typically called from an init() function in the extension package.
func RegisterFilter(f Filter) {
	filtersMu.Lock()
	defer filtersMu.Unlock()
	globalFilters = append(globalFilters, f)
}

// GetFilters returns a snapshot of the current globally registered filter chain.
func GetFilters() FilterChain {
	filtersMu.RLock()
	defer filtersMu.RUnlock()

	chain := make(FilterChain, len(globalFilters))
	copy(chain, globalFilters)
	return chain
}

// Process evaluates all filters in the chain.
func (chain FilterChain) Process(req []byte) (resp []byte, block bool, err error) {
	for _, filter := range chain {
		resp, block, err = filter.Process(req)
		if block || err != nil {
			return resp, block, err
		}
	}
	return nil, false, nil
}

func ForwardWithFilter(chain FilterChain, rawMsg []byte, upstreams []string, timeout time.Duration, dialContext func(ctx context.Context, network, addr string) (net.Conn, error)) ([]byte, error) {
	resp, block, err := chain.Process(rawMsg)
	if block || err != nil {
		return resp, err
	}
	return RaceForward(rawMsg, upstreams, timeout, dialContext)
}
