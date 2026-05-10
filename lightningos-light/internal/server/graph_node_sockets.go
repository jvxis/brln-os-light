package server

import "lightningos-light/internal/lndclient"

func splitGraphNodeSockets(addresses []lndclient.GraphNodeAddress) ([]string, bool) {
	out := make([]string, 0, len(addresses))
	seen := map[string]struct{}{}
	hasOnion := false
	for _, item := range addresses {
		socket := normalizeSocket(item.Addr)
		if socket == "" {
			continue
		}
		if isOnionSocket(socket) {
			hasOnion = true
			continue
		}
		if !socketHasPort(socket) {
			continue
		}
		if _, ok := seen[socket]; ok {
			continue
		}
		seen[socket] = struct{}{}
		out = append(out, socket)
	}
	return out, hasOnion
}
