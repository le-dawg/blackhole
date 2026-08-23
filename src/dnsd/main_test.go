package dnsd

import (
    "net"
    "testing"
    "time"
    "golang.org/x/net/dns/dnsmessage"
)

func TestDNSProxyHandling(t *testing.T) {
    // Setup a dummy upstream UDP DNS server
    upstream, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
    if err != nil {
        t.Fatalf("Failed to listen on ephemeral UDP port: %v", err)
    }
    defer upstream.Close()

    // Handle simple upstream queries and return a mock IP
    go func() {
        buf := make([]byte, 512)
        for {
            n, addr, err := upstream.ReadFromUDP(buf)
            if err != nil {
                return
            }
            var msg dnsmessage.Message
            if err := msg.Unpack(buf[:n]); err != nil {
                continue
            }
            
            // Build mock response
            msg.Header.Response = true
            msg.Answers = append(msg.Answers, dnsmessage.Resource{
                Header: dnsmessage.ResourceHeader{
                    Name:  msg.Questions[0].Name,
                    Type:  dnsmessage.TypeA,
                    Class: dnsmessage.ClassINET,
                    TTL:   60,
                },
                Body: &dnsmessage.AResource{A: [4]byte{9, 9, 9, 9}},
            })
            resp, _ := msg.Pack()
            _, _ = upstream.WriteToUDP(resp, addr)
        }
    }()

    // Create a test resolver
    r := NewFilterEngine([]string{upstream.LocalAddr().String()})
    r.AddBlockedDomain("ads.doubleclick.net")

    // Setup proxy UDP listener on ephemeral port
    proxyConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
    if err != nil {
        t.Fatalf("Failed to start proxy server: %v", err)
    }
    defer proxyConn.Close()

    // Simple handler loop matching the actual main.go logic
    go func() {
        buf := make([]byte, 512)
        for {
            n, cliAddr, err := proxyConn.ReadFromUDP(buf)
            if err != nil {
                return
            }
            
            var msg dnsmessage.Message
            if err := msg.Unpack(buf[:n]); err != nil {
                continue
            }

            domain := msg.Questions[0].Name.String()
            // Normalize trailing dot
            domain = domain[:len(domain)-1]

            // Check blocking
            if r.Resolve(domain) {
                msg.Header.Response = true
                msg.Header.RCode = dnsmessage.RCodeSuccess
                msg.Answers = append(msg.Answers, dnsmessage.Resource{
                    Header: dnsmessage.ResourceHeader{
                        Name:  msg.Questions[0].Name,
                        Type:  dnsmessage.TypeA,
                        Class: dnsmessage.ClassINET,
                        TTL:   3600,
                    },
                    Body: &dnsmessage.AResource{A: [4]byte{0, 0, 0, 0}},
                })
                resp, _ := msg.Pack()
                _, _ = proxyConn.WriteToUDP(resp, cliAddr)
            } else {
                // Forward to upstream
                upConn, err := net.Dial("udp", r.upstreams[0])
                if err != nil {
                    continue
                }
                _, _ = upConn.Write(buf[:n])
                
                respBuf := make([]byte, 512)
                _ = upConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
                rn, err := upConn.Read(respBuf)
                upConn.Close()
                if err == nil {
                    _, _ = proxyConn.WriteToUDP(respBuf[:rn], cliAddr)
                }
            }
        }
    }()

    // Client request helper
    queryMsg := dnsmessage.Message{
        Header: dnsmessage.Header{ID: 1234, RecursionDesired: true},
        Questions: []dnsmessage.Question{
            {
                Name:  dnsmessage.MustNewName("ads.doubleclick.net."),
                Type:  dnsmessage.TypeA,
                Class: dnsmessage.ClassINET,
            },
        },
    }
    queryBytes, _ := queryMsg.Pack()

    client, err := net.Dial("udp", proxyConn.LocalAddr().String())
    if err != nil {
        t.Fatalf("Failed to dial proxy: %v", err)
    }
    defer client.Close()

    // Query 1: Blocked Domain
    _, _ = client.Write(queryBytes)
    respBuf := make([]byte, 512)
    _ = client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
    rn, err := client.Read(respBuf)
    if err != nil {
        t.Fatalf("Failed to read query response: %v", err)
    }

    var respMsg dnsmessage.Message
    if err := respMsg.Unpack(respBuf[:rn]); err != nil {
        t.Fatalf("Failed to unpack response: %v", err)
    }

    if len(respMsg.Answers) == 0 {
        t.Fatal("Expected response answer")
    }
    aRec := respMsg.Answers[0].Body.(*dnsmessage.AResource)
    if aRec.A != [4]byte{0, 0, 0, 0} {
        t.Errorf("Expected blocked query to resolve to 0.0.0.0, got %v", aRec.A)
    }
}
