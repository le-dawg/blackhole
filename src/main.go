package main

import (
    "flag"
    "log"
    "net"
    "os"
    "os/signal"
    "path/filepath"
    "sync"
    "syscall"
    "time"

    "blackhole/src/dnsd"
    "golang.org/x/net/dns/dnsmessage"
)

var (
    upstreamsMu sync.RWMutex
    upstreams   = []string{"1.1.1.1:53", "8.8.8.8:53"}
)

func main() {
    port := flag.Int("port", 5353, "UDP port to listen on")
    exclusionsPathFlag := flag.String("exclusions", "", "Path to exclusions JSON file")
    flag.Parse()

    var err error
    var exclusionsPath string
    if *exclusionsPathFlag != "" {
        exclusionsPath = *exclusionsPathFlag
    } else {
        var homeDir string
        homeDir, err = os.UserHomeDir()
        if err != nil {
            log.Printf("Warning: Failed to get user home directory: %v", err)
            exclusionsPath = "/var/root/Library/Application Support/blackhole/exclusions.json"
        } else {
            exclusionsPath = filepath.Join(homeDir, "Library/Application Support/blackhole/exclusions.json")
        }
    }

    // Initialize resolver with default upstreams
    r := dnsd.NewResolver(upstreams)
    
    // Load standard blocklist domains
    r.AddBlockedDomain("ads.doubleclick.net")
    r.AddBlockedDomain("adservice.google.com")

    // Start SCDynamicStore VPN Monitor to dynamically chain resolver upstreams
    err = dnsd.StartVPNMonitor(func(servers []string) {
        upstreamsMu.Lock()
        defer upstreamsMu.Unlock()
        
        var newUps []string
        for _, s := range servers {
            // Standardize port suffix
            newUps = append(newUps, s+":53")
        }
        if len(newUps) > 0 {
            upstreams = newUps
            log.Printf("VPN Shift detected! Dynamically updated upstreams to: %v", upstreams)
        }
    })
    if err != nil {
        log.Printf("Warning: SCDynamicStore monitor failed to start: %v", err)
    }

    // Setup local UDP listener
    addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: *port}
    conn, err := net.ListenUDP("udp", addr)
    if err != nil {
        log.Fatalf("Failed to listen on UDP port %d: %v", *port, err)
    }
    defer conn.Close()
    log.Printf("Blackhole DNS server listening on 127.0.0.1:%d...", *port)

    // Listen for graceful termination signals
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    go func() {
        <-sigChan
        log.Println("Shutting down daemon...")
        dnsd.StopVPNMonitor()
        conn.Close()
        os.Exit(0)
    }()

    // Proxy server message loop
    buf := make([]byte, 512)
    for {
        n, cliAddr, err := conn.ReadFromUDP(buf)
        if err != nil {
            continue
        }

        var msg dnsmessage.Message
        if err := msg.Unpack(buf[:n]); err != nil {
            continue
        }

        if len(msg.Questions) == 0 {
            continue
        }

        question := msg.Questions[0]
        domain := question.Name.String()
        if len(domain) > 1 && domain[len(domain)-1] == '.' {
            domain = domain[:len(domain)-1]
        }

        // 1. Process matching and exclusions bypass check
        exclusions, _ := dnsd.LoadExclusions(exclusionsPath)
        procName, bundleID, err := dnsd.GetProcessInfoForPort(uint16(cliAddr.Port))
        isExcluded := false
        if err == nil {
            isExcluded = dnsd.IsProcessExcluded(procName, bundleID, exclusions)
        }

        if isExcluded {
            log.Printf("EXCLUSION bypass for process='%s' bundle='%s' domain='%s'", procName, bundleID, domain)
            forwardQuery(buf[:n], cliAddr, conn)
            continue
        }

        // 2. Trie Resolver ad-block list evaluation
        if r.Resolve(domain) {
            log.Printf("BLOCKED domain='%s' client=%s", domain, cliAddr.String())
            sendBlockedResponse(msg, cliAddr, conn)
        } else {
            forwardQuery(buf[:n], cliAddr, conn)
        }
    }
}

func forwardQuery(raw []byte, cliAddr *net.UDPAddr, conn *net.UDPConn) {
    upstreamsMu.RLock()
    currentUpstreams := upstreams
    upstreamsMu.RUnlock()

    for _, target := range currentUpstreams {
        upConn, err := net.Dial("udp", target)
        if err != nil {
            continue
        }
        
        _, err = upConn.Write(raw)
        if err != nil {
            upConn.Close()
            continue
        }

        respBuf := make([]byte, 512)
        _ = upConn.SetReadDeadline(time.Now().Add(1 * time.Second))
        rn, err := upConn.Read(respBuf)
        upConn.Close()
        if err == nil {
            _, _ = conn.WriteToUDP(respBuf[:rn], cliAddr)
            return
        }
    }
}

func sendBlockedResponse(msg dnsmessage.Message, cliAddr *net.UDPAddr, conn *net.UDPConn) {
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
    resp, err := msg.Pack()
    if err == nil {
        _, _ = conn.WriteToUDP(resp, cliAddr)
    }
}
