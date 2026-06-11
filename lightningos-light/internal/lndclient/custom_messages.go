package lndclient

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"lightningos-light/lnrpc"
)

type CustomPeerMessage struct {
	PeerPubkey string
	Type       uint32
	Data       []byte
}

func (c *Client) SendCustomMessage(ctx context.Context, pubkeyHex string, messageType uint32, data []byte) error {
	pubkeyHex = strings.ToLower(strings.TrimSpace(pubkeyHex))
	if pubkeyHex == "" {
		return errors.New("pubkey required")
	}

	pubkey, err := hex.DecodeString(pubkeyHex)
	if err != nil || len(pubkey) != 33 {
		return errors.New("invalid pubkey hex")
	}

	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return err
	}
	defer release()

	client := lnrpc.NewLightningClient(conn)
	_, err = client.SendCustomMessage(ctx, &lnrpc.SendCustomMessageRequest{
		Peer: pubkey,
		Type: messageType,
		Data: data,
	})
	return err
}

func (c *Client) SubscribeCustomMessages(ctx context.Context) (<-chan CustomPeerMessage, <-chan error) {
	msgs := make(chan CustomPeerMessage)
	errs := make(chan error, 1)

	go func() {
		defer close(msgs)
		defer close(errs)

		conn, release, err := c.borrowConn(ctx, grpcRoleAdminStream)
		if err != nil {
			errs <- err
			return
		}
		defer release()

		client := lnrpc.NewLightningClient(conn)
		stream, err := client.SubscribeCustomMessages(ctx, &lnrpc.SubscribeCustomMessagesRequest{})
		if err != nil {
			errs <- err
			return
		}

		for {
			msg, err := stream.Recv()
			if err != nil {
				if ctx.Err() != nil || errors.Is(err, io.EOF) {
					return
				}
				errs <- err
				return
			}
			if msg == nil {
				continue
			}

			peer := ""
			if raw := msg.GetPeer(); len(raw) > 0 {
				peer = strings.ToLower(hex.EncodeToString(raw))
			}
			event := CustomPeerMessage{
				PeerPubkey: peer,
				Type:       msg.GetType(),
				Data:       append([]byte(nil), msg.GetData()...),
			}

			select {
			case msgs <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	return msgs, errs
}
