package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/miekg/dns"
)

type resolver struct {
	mu         sync.RWMutex
	lnd        *lndClient
	pending    *pendingStore
	pubkeys    [][]byte
	ownPubkey  []byte
	keysendAmt int64
	timeout    time.Duration
	qlog       *queryLog
}

func newResolver(lnd *lndClient, pending *pendingStore, cfg *clientConfig, ownPubkeyHex string, qlog *queryLog) *resolver {
	pubkeys := decodePubkeys(cfg.getPubkeys())

	ownPub, err := hex.DecodeString(ownPubkeyHex)
	if err != nil {
		log.Fatalf("invalid own pubkey: %v", err)
	}

	return &resolver{
		lnd:        lnd,
		pending:    pending,
		pubkeys:    pubkeys,
		ownPubkey:  ownPub,
		keysendAmt: cfg.getKeysendAmt(),
		timeout:    time.Duration(cfg.getTimeout()) * time.Second,
		qlog:       qlog,
	}
}

func decodePubkeys(hexes []string) [][]byte {
	out := make([][]byte, 0, len(hexes))
	for _, h := range hexes {
		b, err := hex.DecodeString(h)
		if err != nil {
			log.Printf("warning: invalid server pubkey %s: %v", h, err)
			continue
		}
		out = append(out, b)
	}
	return out
}

func (r *resolver) updateConfig(cfg *clientConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pubkeys = decodePubkeys(cfg.getPubkeys())
	r.keysendAmt = cfg.getKeysendAmt()
	r.timeout = time.Duration(cfg.getTimeout()) * time.Second
}

// Resolve implements the socks5 NameResolver interface.
func (r *resolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	start := time.Now()

	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(name), dns.TypeA)
	msg.RecursionDesired = true

	queryBytes, err := msg.Pack()
	if err != nil {
		return ctx, nil, fmt.Errorf("failed to pack DNS query: %w", err)
	}

	var reqID [16]byte
	if _, err := rand.Read(reqID[:]); err != nil {
		return ctx, nil, fmt.Errorf("failed to generate request ID: %w", err)
	}

	ch := r.pending.register(reqID)

	r.mu.RLock()
	dest := r.pubkeys[0]
	keysendAmt := r.keysendAmt
	timeout := r.timeout
	r.mu.RUnlock()

	records := map[uint64][]byte{
		tlvDNSPayload:   queryBytes,
		tlvReturnPubkey: r.ownPubkey,
		tlvRequestID:    reqID[:],
	}

	destHex := hex.EncodeToString(dest[:8])
	reqIDHex := hex.EncodeToString(reqID[:])
	log.Printf("sending DNS query for %s to %s reqID=%s", name, destHex, reqIDHex)

	if err := r.lnd.sendKeysend(dest, keysendAmt, records); err != nil {
		r.pending.remove(reqID)
		latency := time.Since(start).Milliseconds()
		r.qlog.add(queryEntry{
			Timestamp:    nowTimestamp(),
			Domain:       name,
			ServerPubkey: destHex,
			RequestID:    reqIDHex,
			Status:       "error",
			LatencyMs:    latency,
		})
		return ctx, nil, fmt.Errorf("keysend failed: %w", err)
	}

	select {
	case respBytes := <-ch:
		latency := time.Since(start).Milliseconds()
		resp := new(dns.Msg)
		if err := resp.Unpack(respBytes); err != nil {
			r.qlog.add(queryEntry{
				Timestamp:    nowTimestamp(),
				Domain:       name,
				ServerPubkey: destHex,
				RequestID:    reqIDHex,
				Status:       "error",
				LatencyMs:    latency,
			})
			return ctx, nil, fmt.Errorf("failed to unpack DNS response: %w", err)
		}
		for _, ans := range resp.Answer {
			if a, ok := ans.(*dns.A); ok {
				ip := a.A.String()
				log.Printf("resolved %s -> %s (%dms)", name, ip, latency)
				r.qlog.add(queryEntry{
					Timestamp:    nowTimestamp(),
					Domain:       name,
					ServerPubkey: destHex,
					RequestID:    reqIDHex,
					Status:       "resolved",
					ResolvedIP:   ip,
					LatencyMs:    latency,
				})
				return ctx, a.A, nil
			}
		}
		r.qlog.add(queryEntry{
			Timestamp:    nowTimestamp(),
			Domain:       name,
			ServerPubkey: destHex,
			RequestID:    reqIDHex,
			Status:       "error",
			LatencyMs:    latency,
		})
		return ctx, nil, fmt.Errorf("no A record in DNS response for %s", name)

	case <-time.After(timeout):
		r.pending.remove(reqID)
		latency := time.Since(start).Milliseconds()
		r.qlog.add(queryEntry{
			Timestamp:    nowTimestamp(),
			Domain:       name,
			ServerPubkey: destHex,
			RequestID:    reqIDHex,
			Status:       "timeout",
			LatencyMs:    latency,
		})
		return ctx, nil, fmt.Errorf("DNS resolution timeout for %s", name)

	case <-ctx.Done():
		r.pending.remove(reqID)
		return ctx, nil, ctx.Err()
	}
}
