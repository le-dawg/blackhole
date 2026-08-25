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
				_, _ = serverConn.Write(respBytes)
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

func TestValidateDNSResponse_CNAMEAndBailiwick(t *testing.T) {
	var req dnsmessage.Message
	req.Header.ID = 42
	req.Header.OpCode = 0
	req.Questions = []dnsmessage.Question{
		{
			Name:  dnsmessage.MustNewName("target.example.com."),
			Type:  dnsmessage.TypeA,
			Class: dnsmessage.ClassINET,
		},
	}
	reqRaw, _ := req.Pack()

	// 1. Valid direct answer
	validResp := req
	validResp.Header.Response = true
	validResp.Answers = []dnsmessage.Resource{
		{
			Header: dnsmessage.ResourceHeader{
				Name:  dnsmessage.MustNewName("target.example.com."),
				Type:  dnsmessage.TypeA,
				Class: dnsmessage.ClassINET,
				TTL:   300,
			},
			Body: &dnsmessage.AResource{A: [4]byte{93, 184, 216, 34}},
		},
	}
	respRaw, _ := validResp.Pack()
	if err := validateDNSResponse(reqRaw, respRaw); err != nil {
		t.Fatalf("expected valid response to pass, got err: %v", err)
	}

	// 2. Poisoned answer (out of bailiwick domain)
	poisonResp := validResp
	poisonResp.Answers = []dnsmessage.Resource{
		{
			Header: dnsmessage.ResourceHeader{
				Name:  dnsmessage.MustNewName("bank.com."),
				Type:  dnsmessage.TypeA,
				Class: dnsmessage.ClassINET,
				TTL:   300,
			},
			Body: &dnsmessage.AResource{A: [4]byte{6, 6, 6, 6}},
		},
	}
	poisonRaw, _ := poisonResp.Pack()
	if err := validateDNSResponse(reqRaw, poisonRaw); err == nil {
		t.Fatalf("expected poisoned answer to be rejected")
	}

	// 3. Poisoned authority section
	poisonAuthResp := validResp
	poisonAuthResp.Authorities = []dnsmessage.Resource{
		{
			Header: dnsmessage.ResourceHeader{
				Name:  dnsmessage.MustNewName("unrelated.org."),
				Type:  dnsmessage.TypeNS,
				Class: dnsmessage.ClassINET,
				TTL:   300,
			},
			Body: &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns.evil.com.")},
		},
	}
	poisonAuthRaw, _ := poisonAuthResp.Pack()
	if err := validateDNSResponse(reqRaw, poisonAuthRaw); err == nil {
		t.Fatalf("expected poisoned authority section to be rejected")
	}

	// 4. Poisoned additional section (untrusted glue)
	poisonAddResp := validResp
	poisonAddResp.Additionals = []dnsmessage.Resource{
		{
			Header: dnsmessage.ResourceHeader{
				Name:  dnsmessage.MustNewName("attacker.com."),
				Type:  dnsmessage.TypeA,
				Class: dnsmessage.ClassINET,
				TTL:   300,
			},
			Body: &dnsmessage.AResource{A: [4]byte{1, 1, 1, 1}},
		},
	}
	poisonAddRaw, _ := poisonAddResp.Pack()
	if err := validateDNSResponse(reqRaw, poisonAddRaw); err == nil {
		t.Fatalf("expected poisoned additional record to be rejected")
	}

	// 5. Allowed EDNS0 OPT record in Additionals
	validOptResp := validResp
	validOptResp.Additionals = []dnsmessage.Resource{
		{
			Header: dnsmessage.ResourceHeader{
				Name:  dnsmessage.MustNewName("."),
				Type:  dnsmessage.TypeOPT,
				Class: 4096,
				TTL:   0,
			},
			Body: &dnsmessage.OPTResource{},
		},
	}
	validOptRaw, _ := validOptResp.Pack()
	if err := validateDNSResponse(reqRaw, validOptRaw); err != nil {
		t.Fatalf("expected valid OPT additionals to pass, got: %v", err)
	}
}
