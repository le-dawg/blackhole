package dnsd

import (
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestRaceForward(t *testing.T) {
	// Start two dummy UDP servers
	startDummyServer := func(delay time.Duration, respBytes []byte) string {
		conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			defer conn.Close()
			buf := make([]byte, 512)
			for {
				_, addr, err := conn.ReadFromUDP(buf)
				if err != nil {
					return
				}
				time.Sleep(delay)
				conn.WriteToUDP(respBytes, addr)
				return // handle one req
			}
		}()
		return conn.LocalAddr().String()
	}

	fastResp := []byte("fast response")
	slowResp := []byte("slow response")

	fastServer := startDummyServer(10*time.Millisecond, fastResp)
	slowServer := startDummyServer(100*time.Millisecond, slowResp)

	upstreams := []string{slowServer, fastServer}

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

	resp, err := RaceForward(rawMsg, upstreams, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if string(resp) != "fast response" {
		t.Errorf("expected fast response, got %s", string(resp))
	}
}
