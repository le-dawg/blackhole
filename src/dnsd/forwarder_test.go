package dnsd

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestRaceForward(t *testing.T) {
	// Start two dummy "servers" using pipes
	startDummyServer := func(delay time.Duration, respBytes []byte) (string, func(ctx context.Context, network, addr string) (net.Conn, error)) {
		serverAddr := "dummy" + delay.String()
		
		dialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr != serverAddr {
				return nil, net.ErrClosed
			}
			clientConn, serverConn := net.Pipe()
			go func() {
				defer serverConn.Close()
				buf := make([]byte, 512)
				// simulate read
				_, err := serverConn.Read(buf)
				if err != nil {
					return
				}
				time.Sleep(delay)
				serverConn.Write(respBytes)
			}()
			return clientConn, nil
		}
		
		return serverAddr, dialer
	}

	fastResp := []byte("fast response")
	slowResp := []byte("slow response")

	fastAddr, fastDialer := startDummyServer(10*time.Millisecond, fastResp)
	slowAddr, slowDialer := startDummyServer(100*time.Millisecond, slowResp)

	upstreams := []string{slowAddr, fastAddr}

	// Prepare dummy DNS message bytes
	var msg dnsmessage.Message
	msg.Header.ID = 1234
	msg.Questions = []dnsmessage.Question{
		{
			Name:  dnsmessage.MustNewName("example.com."),
			Type:  dnsmessage.TypeA,
			Class: dnsmessage.ClassINET,
		},
	}
	rawMsg, _ := msg.Pack()

	compositeDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == slowAddr {
			return slowDialer(ctx, network, addr)
		}
		if addr == fastAddr {
			return fastDialer(ctx, network, addr)
		}
		return nil, net.ErrClosed
	}

	resp, err := RaceForward(rawMsg, upstreams, 500*time.Millisecond, compositeDialer)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if string(resp) != "fast response" {
		t.Errorf("expected fast response, got %s", string(resp))
	}
}
