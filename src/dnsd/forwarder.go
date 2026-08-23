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

	if req.Header.ID != resp.Header.ID {
		return errors.New("txid mismatch")
	}

	if len(req.Questions) == 0 || len(resp.Questions) == 0 {
		return errors.New("missing questions")
	}

	q := req.Questions[0]
	rq := resp.Questions[0]
	if q.Name != rq.Name || q.Type != rq.Type {
		return errors.New("qname/qtype mismatch")
	}

	// Bailiwick check logic here
	qNameStr := q.Name.String()
	
	// Pass 1: Build the CNAME chain
	validNames := make(map[string]bool)
	validNames[qNameStr] = true

	changed := true
	for changed {
		changed = false
		for _, ans := range resp.Answers {
			ansName := ans.Header.Name.String()
			if validNames[ansName] {
				if cname, ok := ans.Body.(*dnsmessage.CNAMEResource); ok {
					target := cname.CNAME.String()
					if !validNames[target] {
						validNames[target] = true
						changed = true
					}
				}
			}
		}
	}

	// Pass 2: Validate all answers are in the valid names map
	for _, ans := range resp.Answers {
		ansName := ans.Header.Name.String()
		if !validNames[ansName] && !strings.HasSuffix(ansName, "."+qNameStr) {
			return errors.New("bailiwick mismatch")
		}
	}

	return nil
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
				conn.SetDeadline(deadline)
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
