package dnsd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
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

var ErrUnexpectedIPCServerTermination = errors.New("unexpected ipc server termination")

func NewDaemon(cfg Config) *Daemon {
	return &Daemon{
		config:    cfg,
		dnsCache:  NewDNSCache(4000),
		upstreams: []string{"1.1.1.1:53", "8.8.8.8:53"},
	}
}

func (d *Daemon) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	started := false
	var cleanups []func()
	defer func() {
		if !started {
			for i := len(cleanups) - 1; i >= 0; i-- {
				cleanups[i]()
			}
		}
	}()

	exclusionManager, err := StartExclusionWatcher(d.config.ExclusionsPath)
	if err != nil {
		return err
	}
	cleanups = append(cleanups, func() { exclusionManager.Close() })
	defer exclusionManager.Close()

	r := NewFilterEngine(d.upstreams)

	// Initialize the monitor and fix the leak
	StartProcessMonitor(ctx)
	cleanups = append(cleanups, func() { StopProcessMonitor() })

	if err := StartGravitySync(ctx, d.config.DataDir, r); err != nil {
		return fmt.Errorf("failed to initialize gravity blocklists: %w", err)
	}
	userLists, err := StartUserListWatcher(d.config.DataDir, r)
	if err != nil {
		log.Printf("Warning: failed to start user list watcher: %v", err)
	} else {
		cleanups = append(cleanups, func() { userLists.Close() })
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
	} else {
		cleanups = append(cleanups, func() { StopVPNMonitor() })
	}

	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: d.config.Port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	cleanups = append(cleanups, func() { conn.Close() })
	defer conn.Close()
	log.Printf("Blackhole DNS server listening on 127.0.0.1:%d...", d.config.Port)


	rb := NewRingBuffer(1000)
	var ipcServer *IPCServer
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

	if ipcServer != nil {
		go d.watchIPCServerErrors(ipcServer, cancel)
	}

	started = true
	go d.handleSignals(ctx, cancel, conn, ipcServer)
	d.runMessageLoop(conn, exclusionManager, r, rb, stats)
	return daemonRunResult(ctx)
}

func (d *Daemon) watchIPCServerErrors(ipcServer *IPCServer, cancel context.CancelCauseFunc) {
	err, ok := <-ipcServer.Errors()
	if !ok || err == nil {
		return
	}

	wrappedErr := fmt.Errorf("%w: %v", ErrUnexpectedIPCServerTermination, err)
	log.Printf("IPC server terminated unexpectedly: %v", err)
	cancel(wrappedErr)
}

func daemonRunResult(ctx context.Context) error {
	cause := context.Cause(ctx)
	if cause == nil || errors.Is(cause, context.Canceled) {
		return nil
	}
	return cause
}

func (d *Daemon) handleSignals(ctx context.Context, cancel context.CancelCauseFunc, conn *net.UDPConn, ipcServer *IPCServer) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigChan)
	
	select {
	case sig := <-sigChan:
		log.Printf("Received signal %v. Cleaning up...", sig)
	case <-ctx.Done():
		log.Printf("Context cancelled. Cleaning up...")
	}
	
	StopVPNMonitor()
	StopProcessMonitor()
	
	// Gracefully shut down the IPC HTTP server if it's running
	if ipcServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		ipcServer.Shutdown(shutdownCtx)
	}
	
	// Close the UDP Listener
	conn.Close()
	
	// Ensure the parent context cancels down the tree
	cancel(nil)
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

type dnsQueryJob struct {
	payload []byte
	cliAddr *net.UDPAddr
}

func (d *Daemon) runMessageLoop(conn *net.UDPConn, exclusionManager *ExclusionManager, r *FilterEngine, rb *RingBuffer, stats *GlobalStats) {
	const workerCount = 64
	const queueSize = 2048

	queryQueue := make(chan dnsQueryJob, queueSize)
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range queryQueue {
				d.processQuery(job.payload, job.cliAddr, conn, exclusionManager, r, rb, stats)
			}
		}()
	}

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

		select {
		case queryQueue <- dnsQueryJob{payload: payload, cliAddr: cliAddr}:
		default:
			// Overload: queue is full, return fast SERVFAIL
			d.sendServfail(conn, cliAddr, payload)
		}
	}

	close(queryQueue)
	wg.Wait()
}

func (d *Daemon) sendServfail(conn *net.UDPConn, cliAddr *net.UDPAddr, raw []byte) {
	var msg dnsmessage.Message
	if err := msg.Unpack(raw); err == nil {
		msg.Header.Response = true
		msg.Header.RCode = dnsmessage.RCodeServerFailure
		msg.Answers = nil
		msg.Authorities = nil
		msg.Additionals = nil
		if packed, err := msg.Pack(); err == nil {
			_, _ = conn.WriteToUDP(packed, cliAddr)
		}
	}
}

func (d *Daemon) forwardQuery(raw []byte, cliAddr *net.UDPAddr, conn *net.UDPConn, msg dnsmessage.Message, domain string, chain FilterChain) {
	if len(msg.Questions) > 0 {
		q := msg.Questions[0]
		if cachedMsg, ok := d.dnsCache.Get(domain, uint16(q.Type), uint16(q.Class)); ok {
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
			d.dnsCache.Set(domain, uint16(respMsg.Questions[0].Type), uint16(respMsg.Questions[0].Class), &respMsg)
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
