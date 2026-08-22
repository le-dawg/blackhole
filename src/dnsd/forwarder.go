package dnsd

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

func RaceForward(rawMsg []byte, upstreams []string, timeout time.Duration) ([]byte, error) {
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
			var d net.Dialer
			conn, err := d.DialContext(ctx, "udp", server)
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
				select {
				case resultCh <- respBuf[:n]:
				default:
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
