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
	d.runMessageLoop(ctx, conn, exclusionManager, r, rb, stats)
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

	// Cancel root context first to signal worker queue drain in runMessageLoop
	cancel(nil)

	StopVPNMonitor()
	StopProcessMonitor()

	// Gracefully shut down the IPC HTTP server if it's running
	if ipcServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = ipcServer.Shutdown(shutdownCtx)
	}
}

func (d *Daemon) processQuery(payload []byte, cliAddr *net.UDPAddr, conn *net.UDPConn, exclusionManager *ExclusionManager, r *FilterEngine, rb *RingBuffer, stats *GlobalStats) {
	var msg dnsmessage.Message
	if err := msg.Unpack(payload); err != nil {
		log.Printf("Failed to unpack DNS message: %v", err)
		return
	}

	if len(msg.Questions) != 1 {
		msg.Header.Response = true
		msg.Header.RCode = dnsmessage.RCodeFormatError
		msg.Answers = nil
		msg.Authorities = nil
		msg.Additionals = nil
		if packed, err := msg.Pack(); err == nil {
			_, _ = conn.WriteToUDP(packed, cliAddr)
		}
		stats.Increment(false, "", "Unknown")
		rb.Push(QueryRecord{
			Timestamp:   time.Now(),
			Domain:      "",
			QueryType:   0,
			Status:      "Servfail",
			ProcessName: "Unknown",
			BundleID:    "",
			LatencyMs:   0,
		})
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
	if r.IsBlacklisted(domain) {
		status = "Blocked"
		log.Printf("BLACKLIST blocked domain='%s' client=%s", domain, cliAddr.String())
		sendBlockedResponse(msg, cliAddr, conn)
	} else if IsPaused() {
		if !d.forwardQuery(payload, cliAddr, conn, msg, domain, nil) {
			status = "Servfail"
		} else {
			status = "Allowed"
		}
	} else if isExcluded {
		log.Printf("EXCLUSION bypass for process='%s' bundle='%s' domain='%s'", procName, bundleID, domain)
		if !d.forwardQuery(payload, cliAddr, conn, msg, domain, nil) {
			status = "Servfail"
		} else {
			status = "Excluded"
		}
	} else {
		chain = append(FilterChain{r}, GetFilters()...)

		respRaw, block, err := chain.Process(payload)
		if err != nil {
			status = "Servfail"
			log.Printf("FILTER ERROR domain='%s' client=%s err=%v", domain, cliAddr.String(), err)
			d.sendServfail(conn, cliAddr, payload)
		} else if block {
			status = "Blocked"
			log.Printf("BLOCKED domain='%s' client=%s", domain, cliAddr.String())
			if respRaw != nil {
				_, _ = conn.WriteToUDP(respRaw, cliAddr)
			} else {
				sendBlockedResponse(msg, cliAddr, conn)
			}
		} else {
			if !d.forwardQuery(payload, cliAddr, conn, msg, domain, nil) {
				status = "Servfail"
			} else {
				status = "Allowed"
			}
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

func (d *Daemon) runMessageLoop(ctx context.Context, conn *net.UDPConn, exclusionManager *ExclusionManager, r *FilterEngine, rb *RingBuffer, stats *GlobalStats) {
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

	go func() {
		<-ctx.Done()
		_ = conn.SetReadDeadline(time.Now().Add(1 * time.Millisecond))
	}()

	for {
		buf := make([]byte, 4096)
		n, cliAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				goto shutdown
			default:
			}
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
			// Overload: queue is full, return fast SERVFAIL and record telemetry
			d.sendServfail(conn, cliAddr, payload)
			stats.Increment(false, "", "Unknown")
			rb.Push(QueryRecord{
				Timestamp:   time.Now(),
				Domain:      "",
				QueryType:   0,
				Status:      "Servfail",
				ProcessName: "Unknown",
				BundleID:    "",
				LatencyMs:   0,
			})
		}
	}

shutdown:
	close(queryQueue)
	drainDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(drainDone)
	}()

	select {
	case <-drainDone:
	case <-time.After(3 * time.Second):
		log.Printf("Worker drain timed out after 3s")
	}

	_ = conn.Close()
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

func (d *Daemon) forwardQuery(raw []byte, cliAddr *net.UDPAddr, conn *net.UDPConn, msg dnsmessage.Message, domain string, chain FilterChain) bool {
	if len(msg.Questions) > 0 {
		q := msg.Questions[0]
		cd := msg.Header.CheckingDisabled
		ad := msg.Header.AuthenticData
		if cachedMsg, ok := d.dnsCache.Get(domain, uint16(q.Type), uint16(q.Class), cd, ad); ok {
			cp := *cachedMsg
			cp.Header.ID = msg.Header.ID
			resp, err := cp.Pack()
			if err == nil {
				_, _ = conn.WriteToUDP(resp, cliAddr)
				return true
			}
		}
	}

	d.upstreamMu.RLock()
	currentUpstreams := d.upstreams
	d.upstreamMu.RUnlock()

	respRaw, err := ForwardWithFilter(chain, raw, currentUpstreams, 500*time.Millisecond, nil)
	if err != nil {
		d.sendServfail(conn, cliAddr, raw)
		return false
	}

	var respMsg dnsmessage.Message
	if unpackErr := respMsg.Unpack(respRaw); unpackErr == nil && len(respMsg.Questions) > 0 {
		cd := msg.Header.CheckingDisabled
		ad := msg.Header.AuthenticData
		d.dnsCache.Set(domain, uint16(respMsg.Questions[0].Type), uint16(respMsg.Questions[0].Class), cd, ad, &respMsg)
	}
	_, _ = conn.WriteToUDP(respRaw, cliAddr)
	return true
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
