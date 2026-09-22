package server

import (
	"strings"

	"lightningos-light/internal/lndclient"
)

// LND can return its alias lookup failure in the peer_alias field itself.
// Absence of an alias says nothing about the channel's visibility.
func notificationPeerAlias(alias string) string {
	alias = strings.TrimSpace(alias)
	if strings.HasPrefix(strings.ToLower(alias), "unable to lookup peer alias:") {
		return ""
	}
	return alias
}

func (n *Notifier) channelPeerAlias(alias, pubkey string) string {
	if alias = notificationPeerAlias(alias); alias != "" {
		return alias
	}
	return notificationPeerAlias(n.lookupNodeAlias(pubkey))
}

func (n *Notifier) enrichPendingChannelNotification(evt *Notification, info *lndclient.PendingChannelInfo) {
	if info == nil {
		return
	}
	if info.CapacitySat > 0 {
		evt.AmountSat = info.CapacitySat
	}
	evt.PeerPubkey = info.RemotePubkey
	evt.PeerAlias = n.channelPeerAlias(info.PeerAlias, info.RemotePubkey)
	private := info.Private
	evt.ChannelPrivate = &private
	if evt.ChannelPoint == "" {
		evt.ChannelPoint = info.ChannelPoint
	}
}
