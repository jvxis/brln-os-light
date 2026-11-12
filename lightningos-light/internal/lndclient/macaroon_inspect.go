package lndclient

import (
	"context"
	"errors"
	"strconv"

	"google.golang.org/protobuf/proto"
	"gopkg.in/macaroon.v2"
	"lightningos-light/lnrpc"
)

// InspectMacaroon decodes metadata, not authenticity. Callers must authenticate
// against LND before trusting a root key ID for any revocation operation.
func InspectMacaroon(raw []byte) (uint64, []MacaroonPermission, error) {
	invalid := errors.New("unsupported LND credential")
	if len(raw) == 0 || len(raw) > 64*1024 {
		return 0, nil, invalid
	}
	var mac macaroon.Macaroon
	if mac.UnmarshalBinary(raw) != nil || len(mac.Caveats()) != 0 {
		return 0, nil, invalid
	}
	id := mac.Id()
	// LND uses bakery v3 identifiers: one version byte followed by MacaroonId.
	if len(id) < 2 || id[0] != 3 {
		return 0, nil, invalid
	}
	var metadata lnrpc.MacaroonId
	if proto.Unmarshal(id[1:], &metadata) != nil {
		return 0, nil, invalid
	}
	root, err := strconv.ParseUint(string(metadata.StorageId), 10, 64)
	if err != nil || strconv.FormatUint(root, 10) != string(metadata.StorageId) {
		return 0, nil, invalid
	}
	var permissions []MacaroonPermission
	for _, op := range metadata.Ops {
		for _, action := range op.GetActions() {
			permissions = append(permissions, MacaroonPermission{Entity: op.GetEntity(), Action: action})
		}
	}
	permissions, err = NormalizeMacaroonPermissions(permissions)
	if err != nil {
		return 0, nil, invalid
	}
	return root, permissions, nil
}

// AuthenticateMacaroon asks LND to verify the signature and requested authority.
// No wallet/channel operation is executed.
func (c *Client) AuthenticateMacaroon(ctx context.Context, raw []byte, permissions []MacaroonPermission) error {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()
	req := &lnrpc.CheckMacPermRequest{Macaroon: raw, FullMethod: "/lnrpc.Lightning/GetInfo"}
	for _, p := range permissions {
		req.Permissions = append(req.Permissions, &lnrpc.MacaroonPermission{Entity: p.Entity, Action: p.Action})
	}
	response, err := lnrpc.NewLightningClient(conn).CheckMacaroonPermissions(ctx, req)
	if err != nil || !response.GetValid() {
		return errors.New("LND credential authentication failed")
	}
	return nil
}
