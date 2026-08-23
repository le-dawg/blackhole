package dnsd

import (
	"bytes"
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

	// Prepare dummy DNS message bytes
	var msg dnsmessage.Message
	msg.Header.ID = 1234
	msg.Header.Response = true
	msg.Questions = []dnsmessage.Question{
		{
			Name:  dnsmessage.MustNewName("example.com."),
			Type:  dnsmessage.TypeA,
			Class: dnsmessage.ClassINET,
		},
	}
	rawMsg, _ := msg.Pack()
	
	msgFast := msg
	msgFast.Answers = []dnsmessage.Resource{
		{
			Header: dnsmessage.ResourceHeader{
				Name:  msg.Questions[0].Name,
				Type:  dnsmessage.TypeA,
				Class: dnsmessage.ClassINET,
				TTL:   60,
			},
			Body: &dnsmessage.AResource{A: [4]byte{1, 2, 3, 4}},
		},
	}
	fastResp, _ := msgFast.Pack()

	msgSlow := msg
	msgSlow.Answers = []dnsmessage.Resource{
		{
			Header: dnsmessage.ResourceHeader{
				Name:  msg.Questions[0].Name,
				Type:  dnsmessage.TypeA,
				Class: dnsmessage.ClassINET,
				TTL:   60,
			},
			Body: &dnsmessage.AResource{A: [4]byte{5, 6, 7, 8}},
		},
	}
	slowResp, _ := msgSlow.Pack()

	fastAddr, fastDialer := startDummyServer(10*time.Millisecond, fastResp)
	slowAddr, slowDialer := startDummyServer(100*time.Millisecond, slowResp)

	upstreams := []string{slowAddr, fastAddr}

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

	if !bytes.Equal(resp, fastResp) {
		t.Errorf("expected fast response, got %v", resp)
	}
}
