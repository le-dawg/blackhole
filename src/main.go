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
    dnsCache *dnsd.DNSCache = dnsd.NewDNSCache(4000)

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

    exclusionManager, err := dnsd.StartExclusionWatcher(exclusionsPath)
    if err != nil {
        log.Fatalf("Failed to start exclusion watcher: %v", err)
    }
    defer exclusionManager.Close()

    // Initialize resolver with default upstreams
    r := dnsd.NewResolver(upstreams)
    
    // Load standard blocklist domains
    dnsd.StartGravitySync(filepath.Dir(exclusionsPath), r) // Using the directory of exclusionsPath
    userLists, err := dnsd.StartUserListWatcher(filepath.Dir(exclusionsPath), r)
    if err != nil {
        log.Printf("Warning: failed to start user list watcher: %v", err)
    } else {
        defer userLists.Close()
    }

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
        procName, bundleID, err := dnsd.GetProcessInfoForPort(uint16(cliAddr.Port), exclusionManager.GetCliPatterns())
        isExcluded := false
        if err == nil {
            isExcluded = exclusionManager.IsExcluded(procName, bundleID)
        }

        if isExcluded {
            log.Printf("EXCLUSION bypass for process='%s' bundle='%s' domain='%s'", procName, bundleID, domain)
            forwardQuery(buf[:n], cliAddr, conn, msg, domain)
            continue
        }

        // 2. Trie Resolver ad-block list evaluation
        if r.Resolve(domain) {
            log.Printf("BLOCKED domain='%s' client=%s", domain, cliAddr.String())
            sendBlockedResponse(msg, cliAddr, conn)
        } else {
            forwardQuery(buf[:n], cliAddr, conn, msg, domain)
        }
    }
}

func forwardQuery(raw []byte, cliAddr *net.UDPAddr, conn *net.UDPConn, msg dnsmessage.Message, domain string) {
    if len(msg.Questions) > 0 {
        q := msg.Questions[0]
        if cachedMsg, ok := dnsCache.Get(domain, uint16(q.Type)); ok {
            cachedMsg.Header.ID = msg.Header.ID
            resp, err := cachedMsg.Pack()
            if err == nil {
                _, _ = conn.WriteToUDP(resp, cliAddr)
                return
            }
        }
    }

    upstreamsMu.RLock()
    currentUpstreams := upstreams
    upstreamsMu.RUnlock()

    respRaw, err := dnsd.RaceForward(raw, currentUpstreams, 500*time.Millisecond)
    if err == nil {
        var respMsg dnsmessage.Message
        if unpackErr := respMsg.Unpack(respRaw); unpackErr == nil && len(respMsg.Questions) > 0 {
            dnsCache.Set(domain, uint16(respMsg.Questions[0].Type), &respMsg)
        }
        _, _ = conn.WriteToUDP(respRaw, cliAddr)
    }
}

func sendBlockedResponse(msg dnsmessage.Message, cliAddr *net.UDPAddr, conn *net.UDPConn) {
    msg.Header.Response = true
    msg.Header.RCode = dnsmessage.RCodeSuccess
    msg.Answers = nil

    for _, q := range msg.Questions {
        switch q.Type {
        case dnsmessage.TypeA:
            msg.Answers = append(msg.Answers, dnsmessage.Resource{
                Header: dnsmessage.ResourceHeader{
                    Name:  q.Name,
                    Type:  dnsmessage.TypeA,
                    Class: dnsmessage.ClassINET,
                    TTL:   3600,
                },
                Body: &dnsmessage.AResource{A: [4]byte{0, 0, 0, 0}},
            })
        case dnsmessage.TypeAAAA:
            msg.Answers = append(msg.Answers, dnsmessage.Resource{
                Header: dnsmessage.ResourceHeader{
                    Name:  q.Name,
                    Type:  dnsmessage.TypeAAAA,
                    Class: dnsmessage.ClassINET,
                    TTL:   3600,
                },
                Body: &dnsmessage.AAAAResource{AAAA: [16]byte{}},
            })
        case dnsmessage.Type(65), dnsmessage.Type(64):
            // HTTPS, SVCB - empty NOERROR response
        case dnsmessage.TypeALL:
            msg.Answers = append(msg.Answers, dnsmessage.Resource{
                Header: dnsmessage.ResourceHeader{
                    Name:  q.Name,
                    Type:  dnsmessage.TypeA,
                    Class: dnsmessage.ClassINET,
                    TTL:   3600,
                },
                Body: &dnsmessage.AResource{A: [4]byte{0, 0, 0, 0}},
            })
            msg.Answers = append(msg.Answers, dnsmessage.Resource{
                Header: dnsmessage.ResourceHeader{
                    Name:  q.Name,
                    Type:  dnsmessage.TypeAAAA,
                    Class: dnsmessage.ClassINET,
                    TTL:   3600,
                },
                Body: &dnsmessage.AAAAResource{AAAA: [16]byte{}},
            })
        }
    }

    resp, err := msg.Pack()
    if err == nil {
        _, _ = conn.WriteToUDP(resp, cliAddr)
    }
}
