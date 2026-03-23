package main

import (
	"log"
	"os"

	"github.com/armon/go-socks5"
)

func startSOCKS5(listen string, r *resolver) {
	conf := &socks5.Config{
		Resolver: r,
		Logger:   log.New(os.Stdout, "socks5: ", log.LstdFlags),
	}

	server, err := socks5.New(conf)
	if err != nil {
		log.Fatalf("failed to create SOCKS5 server: %v", err)
	}

	log.Printf("SOCKS5 listening on %s", listen)
	if err := server.ListenAndServe("tcp", listen); err != nil {
		log.Fatalf("SOCKS5 server failed: %v", err)
	}
}
