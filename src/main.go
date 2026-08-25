package main

import (
	"context"
	"flag"
	"log"

	"blackhole/src/dnsd"
)

func main() {
	port := flag.Int("port", 5353, "UDP port to listen on")
	exclusionsPathFlag := flag.String("exclusions", "", "Path to exclusions JSON file")
	flag.Parse()

	cfg := dnsd.DefaultConfig()
	if *port != 5353 {
		cfg.Port = *port
	}
	if *exclusionsPathFlag != "" {
		cfg.SetExclusionsPath(*exclusionsPathFlag)
	}

	daemon := dnsd.NewDaemon(cfg)
	if err := daemon.Start(context.Background()); err != nil {
		log.Fatalf("Daemon error: %v", err)
	}
}
