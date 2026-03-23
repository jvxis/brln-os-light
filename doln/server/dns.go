package main

import (
	"fmt"
	"sync"

	"github.com/miekg/dns"
)

type dnsResolver struct {
	mu     sync.RWMutex
	addr   string
	client *dns.Client
}

func newDNSResolver(addr string) *dnsResolver {
	return &dnsResolver{
		addr:   addr,
		client: &dns.Client{Net: "udp"},
	}
}

func (r *dnsResolver) resolve(queryBytes []byte) ([]byte, error) {
	msg := new(dns.Msg)
	if err := msg.Unpack(queryBytes); err != nil {
		return nil, fmt.Errorf("failed to unpack DNS query: %w", err)
	}

	r.mu.RLock()
	addr := r.addr
	r.mu.RUnlock()

	resp, _, err := r.client.Exchange(msg, addr)
	if err != nil {
		return nil, fmt.Errorf("DNS exchange failed: %w", err)
	}

	packed, err := resp.Pack()
	if err != nil {
		return nil, fmt.Errorf("failed to pack DNS response: %w", err)
	}

	return packed, nil
}

func (r *dnsResolver) updateAddr(addr string) {
	r.mu.Lock()
	r.addr = addr
	r.mu.Unlock()
}
