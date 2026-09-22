package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"

	"github.com/jackc/pgx/v5/pgtype"
)

const missingNotificationAlias = "unable to lookup peer alias: alias for node not found"
const notificationTestPubkey = "02abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

func TestChannelNotificationVisibilityAndAlias(t *testing.T) {
	for _, private := range []bool{false, true} {
		for _, alias := range []string{"Known peer", "", missingNotificationAlias} {
			t.Run(fmt.Sprintf("private=%t/alias=%s", private, alias), func(t *testing.T) {
				n := &Notifier{}
				wantAlias := alias
				if alias == "" || alias == missingNotificationAlias {
					wantAlias = shortPubKey(notificationTestPubkey)
				}
				pending := Notification{Type: "channel", Action: "opening", Status: "PENDING"}
				n.enrichPendingChannelNotification(&pending, &lndclient.PendingChannelInfo{
					Private: private, PeerAlias: alias, RemotePubkey: notificationTestPubkey,
					CapacitySat: 2096796, ChannelPoint: "funding:0",
				})
				opened, key := n.channelEventToNotification(&lnrpc.ChannelEventUpdate{
					Type: lnrpc.ChannelEventUpdate_OPEN_CHANNEL,
					Channel: &lnrpc.ChannelEventUpdate_OpenChannel{OpenChannel: &lnrpc.Channel{
						Private: private, PeerAlias: alias, RemotePubkey: notificationTestPubkey,
						Capacity: 2096796, ChannelPoint: "funding:0",
					}},
				})
				if key != "channel:open:funding:0" {
					t.Fatalf("unexpected event key: %s", key)
				}
				for _, evt := range []Notification{pending, opened} {
					if evt.ChannelPrivate == nil || *evt.ChannelPrivate != private || evt.PeerAlias != wantAlias || evt.AmountSat != 2096796 || evt.ChannelPoint != "funding:0" {
						t.Fatalf("incorrect channel metadata: %+v", evt)
					}
					msg := telegramActivityMirrorMessage(evt)
					if strings.Contains(msg, "unable to lookup") || !strings.Contains(msg, wantAlias) || strings.Contains(msg, "Private channel") != private {
						t.Fatalf("incorrect Telegram message: %s", msg)
					}
					filename, caption := telegramBackupPayload(evt.Action, evt.ChannelPoint, "My node", evt.PeerAlias, time.Date(2026, 9, 22, 18, 0, 40, 0, time.UTC), evt.ChannelPrivate)
					if !strings.HasPrefix(filename, "scb-"+evt.Action+"-") || !strings.Contains(caption, "peer "+wantAlias) || !strings.Contains(caption, "channel funding:0") || strings.Contains(caption, "Private channel") != private {
						t.Fatalf("incorrect SCB payload: %s / %s", filename, caption)
					}
				}
			})
		}
	}
}

func TestChannelNotificationUnknownVisibility(t *testing.T) {
	evt := Notification{Type: "channel", Action: "opening", PeerPubkey: notificationTestPubkey, PeerAlias: missingNotificationAlias}
	(&Notifier{}).enrichPendingChannelNotification(&evt, nil)
	if evt.ChannelPrivate != nil {
		t.Fatal("missing metadata must not infer channel visibility")
	}
	msg := telegramActivityMirrorMessage(evt)
	if strings.Contains(msg, "Private") || strings.Contains(msg, "unable to lookup") || !strings.Contains(msg, shortPubKey(notificationTestPubkey)) {
		t.Fatalf("incorrect fallback message: %s", msg)
	}
}

type notificationScanFunc func(...any) error

func (f notificationScanFunc) Scan(dest ...any) error { return f(dest...) }

func TestScanNotificationChannelVisibility(t *testing.T) {
	// Exercise pgx's actual nullable-bool decoding for both list and upsert rows.
	for _, value := range []string{"t", "f", "null"} {
		for _, withInserted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/inserted=%t", value, withInserted), func(t *testing.T) {
				scanner := notificationScanFunc(func(dest ...any) error {
					want := 18
					if withInserted {
						want++
					}
					if len(dest) != want {
						return fmt.Errorf("scan columns = %d, want %d", len(dest), want)
					}
					*dest[2].(*string) = "channel"
					*dest[10].(*pgtype.Text) = pgtype.Text{String: missingNotificationAlias, Valid: true}
					var raw []byte
					if value != "null" {
						raw = []byte(value)
					}
					if withInserted {
						*dest[18].(*bool) = true
					}
					return pgtype.NewMap().Scan(pgtype.BoolOID, pgtype.TextFormatCode, raw, dest[17])
				})
				var evt Notification
				var err error
				if withInserted {
					var inserted bool
					evt, inserted, err = scanNotificationWithInserted(scanner)
					if !inserted {
						t.Fatal("inserted flag lost")
					}
				} else {
					evt, err = scanNotification(scanner)
				}
				if err != nil {
					t.Fatal(err)
				}
				if evt.PeerAlias != "" {
					t.Fatal("legacy alias error leaked")
				}
				if value == "null" {
					if evt.ChannelPrivate != nil {
						t.Fatal("legacy event must retain unknown visibility")
					}
				} else if evt.ChannelPrivate == nil || *evt.ChannelPrivate != (value == "t") {
					t.Fatal("channel visibility lost during scan")
				}
				payload, err := json.Marshal(evt)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(payload), "channel_private") != (value != "null") {
					t.Fatalf("incorrect API visibility: %s", payload)
				}
			})
		}
	}
}
