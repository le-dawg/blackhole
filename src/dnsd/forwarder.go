package dnsd

import (
	"bytes"
	"context"
	"errors"
	"net"
	
	"sync"
	"time"
)

func validateDNSResponse(req, resp []byte) error {
	if len(req) < 12 || len(resp) < 12 {
		return errors.New("invalid length")
	}
	// 1. TXID
	if req[0] != resp[0] || req[1] != resp[1] {
		return errors.New("txid mismatch")
	}
	
	// Fast-path bailiwick: we just require the response to be somewhat sane for now
	// Ideally we parse the QNAME. We'll do a simple substring match for the QNAME bytes.
	// Find null terminator of QNAME in req
	idx := 12
	for idx < len(req) && req[idx] != 0 {
		idx += int(req[idx]) + 1
	}
	if idx+5 > len(req) {
		return errors.New("invalid request qname")
	}
	qname := req[12 : idx+1]
	
	// QNAME should be in response
	if !bytes.Contains(resp, qname) {
		return errors.New("bailiwick mismatch")
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
