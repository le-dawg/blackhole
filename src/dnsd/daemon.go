package dnsd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type Daemon struct {
	config     Config
	dnsCache   *DNSCache
	upstreams  []string
	upstreamMu sync.RWMutex
}

func NewDaemon(cfg Config) *Daemon {
	return &Daemon{
		config:    cfg,
		dnsCache:  NewDNSCache(4000),
		upstreams: []string{"1.1.1.1:53", "8.8.8.8:53"},
	}
}

func (d *Daemon) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	exclusionManager, err := StartExclusionWatcher(d.config.ExclusionsPath)
	if err != nil {
		return err
	}
	defer exclusionManager.Close()

	r := NewFilterEngine(d.upstreams)

	// Initialize the monitor and fix the leak
	StartProcessMonitor(ctx)
	if err := StartGravitySync(ctx, d.config.DataDir, r); err != nil {
		return fmt.Errorf("failed to initialize gravity blocklists: %w", err)
	}
	userLists, err := StartUserListWatcher(d.config.DataDir, r)
	if err != nil {
		log.Printf("Warning: failed to start user list watcher: %v", err)
	} else {
		defer userLists.Close()
	}

	err = StartVPNMonitor(func(servers []string) {
		d.upstreamMu.Lock()
		defer d.upstreamMu.Unlock()

		var newUps []string
		for _, s := range servers {
			newUps = append(newUps, s+":53")
		}
		if len(newUps) > 0 {
			d.upstreams = newUps
			log.Printf("VPN Shift detected! Dynamically updated upstreams to: %v", d.upstreams)
		}
	})
	if err != nil {
		log.Printf("Warning: SCDynamicStore monitor failed to start: %v", err)
	}

	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: d.config.Port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	log.Printf("Blackhole DNS server listening on 127.0.0.1:%d...", d.config.Port)


	rb := NewRingBuffer(1000)
	var ipcServer *http.Server
	stats := NewGlobalStats()

	if d.config.SocketPath != "" {
		os.Remove(d.config.SocketPath)
		ipcListener, err := net.Listen("unix", d.config.SocketPath)
		if err != nil {
			return fmt.Errorf("failed to bind IPC socket at %s: %w", d.config.SocketPath, err)
		}
		if err := os.Chmod(d.config.SocketPath, 0600); err != nil {
			ipcListener.Close()
			return fmt.Errorf("failed to chmod IPC socket: %w", err)
		}
		if consoleStat, err := os.Stat("/dev/console"); err == nil {
			if sysStat, ok := consoleStat.Sys().(*syscall.Stat_t); ok {
				if err := os.Chown(d.config.SocketPath, int(sysStat.Uid), int(sysStat.Gid)); err != nil {
					log.Printf("Warning: failed to chown IPC socket: %v", err)
				}
			}
		}
		ipcServer, err = StartIPCServer(ipcListener, rb, stats)
		if err != nil {
			ipcListener.Close()
			return fmt.Errorf("failed to start IPC server: %w", err)
		}
	}

	go d.handleSignals(ctx, cancel, conn, ipcServer)
	d.runMessageLoop(conn, exclusionManager, r, rb, stats)
	return nil
}

func (d *Daemon) handleSignals(ctx context.Context, cancel context.CancelFunc, conn *net.UDPConn, ipcServer *http.Server) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	
	select {
	case sig := <-sigChan:
		log.Printf("Received signal %v. Cleaning up...", sig)
	case <-ctx.Done():
		log.Printf("Context cancelled. Cleaning up...")
	}
	
	StopVPNMonitor()
	
	// Gracefully shut down the IPC HTTP server if it's running
	if ipcServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		ipcServer.Shutdown(shutdownCtx)
	}
	
	// Close the UDP Listener
	conn.Close()
	
	// Ensure the parent context cancels down the tree
	cancel()
}

func (d *Daemon) processQuery(payload []byte, cliAddr *net.UDPAddr, conn *net.UDPConn, exclusionManager *ExclusionManager, r *FilterEngine, rb *RingBuffer, stats *GlobalStats) {
	var msg dnsmessage.Message
	if err := msg.Unpack(payload); err != nil {
		return
	}

	if len(msg.Questions) == 0 {
		return
	}

	question := msg.Questions[0]
	domain := question.Name.String()
	if len(domain) > 1 && domain[len(domain)-1] == '.' {
		domain = domain[:len(domain)-1]
	}

	startTime := time.Now()

	procName, bundleID, err := GetProcessInfoForPort(uint16(cliAddr.Port), exclusionManager.GetCliPatterns())
	if err != nil || procName == "" {
		procName = "Unknown"
	}
	isExcluded := false
	if procName != "Unknown" {
		isExcluded = exclusionManager.IsExcluded(procName, bundleID)
	}

	var status string

	var chain FilterChain
	if IsPaused() {
		status = "Allowed"
		d.forwardQuery(payload, cliAddr, conn, msg, domain, nil)
	} else if isExcluded {
		status = "Excluded"
		log.Printf("EXCLUSION bypass for process='%s' bundle='%s' domain='%s'", procName, bundleID, domain)
		d.forwardQuery(payload, cliAddr, conn, msg, domain, nil)
	} else {
		// FIXED: Append GetFilters() to the chain so registered extensions actually run.
		chain = append(FilterChain{r}, GetFilters()...)
		
		// Evaluate the full FilterChain *before* recording stats
		respRaw, block, err := chain.Process(payload)
		if block || err != nil {
			status = "Blocked"
			if err != nil {
				log.Printf("BLOCKED (error) domain='%s' client=%s err=%v", domain, cliAddr.String(), err)
			} else {
				log.Printf("BLOCKED domain='%s' client=%s", domain, cliAddr.String())
			}
			if respRaw != nil {
				_, _ = conn.WriteToUDP(respRaw, cliAddr)
			}
		} else {
			status = "Allowed"
			// Pass a nil chain because filters have already been applied
			d.forwardQuery(payload, cliAddr, conn, msg, domain, nil)
		}
	}

	latencyMs := float64(time.Since(startTime).Microseconds()) / 1000.0
	stats.Increment(status == "Blocked", domain, procName)
	rb.Push(QueryRecord{
		Timestamp:   time.Now(),
		Domain:      domain,
		QueryType:   uint16(question.Type),
		Status:      status,
		ProcessName: procName,
		BundleID:    bundleID,
		LatencyMs:   latencyMs,
	})
}

func (d *Daemon) runMessageLoop(conn *net.UDPConn, exclusionManager *ExclusionManager, r *FilterEngine, rb *RingBuffer, stats *GlobalStats) {
	for {
		buf := make([]byte, 4096)
		n, cliAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			log.Printf("Error reading UDP: %v", err)
			continue
		}

		payload := make([]byte, n)
		copy(payload, buf[:n])

		go d.processQuery(payload, cliAddr, conn, exclusionManager, r, rb, stats)
	}
}

func (d *Daemon) forwardQuery(raw []byte, cliAddr *net.UDPAddr, conn *net.UDPConn, msg dnsmessage.Message, domain string, chain FilterChain) {
	if len(msg.Questions) > 0 {
		q := msg.Questions[0]
		if cachedMsg, ok := d.dnsCache.Get(domain, uint16(q.Type)); ok {
			cp := *cachedMsg
			cp.Header.ID = msg.Header.ID
			resp, err := cp.Pack()
			if err == nil {
				_, _ = conn.WriteToUDP(resp, cliAddr)
				return
			}
		}
	}

	d.upstreamMu.RLock()
	currentUpstreams := d.upstreams
	d.upstreamMu.RUnlock()

	respRaw, err := ForwardWithFilter(chain, raw, currentUpstreams, 500*time.Millisecond, nil)
	if err == nil {
		var respMsg dnsmessage.Message
		if unpackErr := respMsg.Unpack(respRaw); unpackErr == nil && len(respMsg.Questions) > 0 {
			d.dnsCache.Set(domain, uint16(respMsg.Questions[0].Type), &respMsg)
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
			// HTTPS, SVCB
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
