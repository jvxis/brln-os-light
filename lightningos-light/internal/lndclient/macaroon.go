package lndclient

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"lightningos-light/lnrpc"
)

type MacaroonPermission struct {
	Entity string `json:"entity"`
	Action string `json:"action"`
}

type BakeCustomMacaroonRequest struct {
	Permissions              []MacaroonPermission
	RootKeyID                uint64
	AllowExternalPermissions bool
}

type BakeCustomMacaroonResult struct {
	FileName       string               `json:"file_name"`
	RootKeyID      uint64               `json:"root_key_id"`
	Permissions    []MacaroonPermission `json:"permissions"`
	MacaroonHex    string               `json:"macaroon_hex"`
	MacaroonBase64 string               `json:"macaroon_base64"`
}

func (c *Client) ListMacaroonPermissions(ctx context.Context) ([]MacaroonPermission, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.ListPermissions(ctx, &lnrpc.ListPermissionsRequest{})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	permissions := make([]MacaroonPermission, 0)
	for _, list := range resp.GetMethodPermissions() {
		for _, item := range list.GetPermissions() {
			perm, err := NormalizeMacaroonPermission(MacaroonPermission{
				Entity: item.GetEntity(),
				Action: item.GetAction(),
			})
			if err != nil {
				continue
			}
			key := MacaroonPermissionKey(perm)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			permissions = append(permissions, perm)
		}
	}
	sort.Slice(permissions, func(i, j int) bool {
		if permissions[i].Entity == permissions[j].Entity {
			return permissions[i].Action < permissions[j].Action
		}
		return permissions[i].Entity < permissions[j].Entity
	})
	return permissions, nil
}

func (c *Client) ListMacaroonIDs(ctx context.Context) ([]uint64, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.ListMacaroonIDs(ctx, &lnrpc.ListMacaroonIDsRequest{})
	if err != nil {
		return nil, err
	}
	ids := append([]uint64(nil), resp.GetRootKeyIds()...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func (c *Client) DeleteMacaroonID(ctx context.Context, rootKeyID uint64) error {
	if rootKeyID == 0 {
		return errors.New("root key ID must be positive")
	}
	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	response, err := client.DeleteMacaroonID(ctx, &lnrpc.DeleteMacaroonIDRequest{RootKeyId: rootKeyID})
	if err != nil {
		return err
	}
	if !response.GetDeleted() {
		return errors.New("LND macaroon root key was not deleted")
	}
	return nil
}

func (c *Client) BakeCustomMacaroon(ctx context.Context, params BakeCustomMacaroonRequest) (BakeCustomMacaroonResult, error) {
	permissions, err := NormalizeMacaroonPermissions(params.Permissions)
	if err != nil {
		return BakeCustomMacaroonResult{}, err
	}
	if params.RootKeyID == 0 {
		return BakeCustomMacaroonResult{}, errors.New("root key ID must be positive")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return BakeCustomMacaroonResult{}, err
	}
	defer conn.Close()

	req := &lnrpc.BakeMacaroonRequest{
		RootKeyId:                params.RootKeyID,
		AllowExternalPermissions: params.AllowExternalPermissions,
		Permissions:              make([]*lnrpc.MacaroonPermission, 0, len(permissions)),
	}
	for _, perm := range permissions {
		req.Permissions = append(req.Permissions, &lnrpc.MacaroonPermission{
			Entity: perm.Entity,
			Action: perm.Action,
		})
	}

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.BakeMacaroon(ctx, req)
	if err != nil {
		return BakeCustomMacaroonResult{}, err
	}

	macaroonHex := strings.TrimSpace(resp.GetMacaroon())
	macaroonBase64, err := MacaroonHexToBase64(macaroonHex)
	if err != nil {
		return BakeCustomMacaroonResult{}, err
	}

	return BakeCustomMacaroonResult{
		FileName:       CustomMacaroonFileName(params.RootKeyID, time.Now()),
		RootKeyID:      params.RootKeyID,
		Permissions:    permissions,
		MacaroonHex:    macaroonHex,
		MacaroonBase64: macaroonBase64,
	}, nil
}

func NormalizeMacaroonPermissions(input []MacaroonPermission) ([]MacaroonPermission, error) {
	if len(input) == 0 {
		return nil, errors.New("permissions required")
	}
	seen := make(map[string]struct{}, len(input))
	permissions := make([]MacaroonPermission, 0, len(input))
	for _, item := range input {
		perm, err := NormalizeMacaroonPermission(item)
		if err != nil {
			return nil, err
		}
		key := MacaroonPermissionKey(perm)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		permissions = append(permissions, perm)
	}
	if len(permissions) == 0 {
		return nil, errors.New("permissions required")
	}
	return permissions, nil
}

func NormalizeMacaroonPermission(input MacaroonPermission) (MacaroonPermission, error) {
	entity, err := normalizeMacaroonPermissionToken(input.Entity, "permission entity")
	if err != nil {
		return MacaroonPermission{}, err
	}
	action, err := normalizeMacaroonPermissionToken(input.Action, "permission action")
	if err != nil {
		return MacaroonPermission{}, err
	}
	return MacaroonPermission{Entity: entity, Action: action}, nil
}

func MacaroonPermissionKey(permission MacaroonPermission) string {
	return strings.TrimSpace(permission.Entity) + ":" + strings.TrimSpace(permission.Action)
}

func MacaroonPermissionStrings(permissions []MacaroonPermission) []string {
	out := make([]string, 0, len(permissions))
	for _, perm := range permissions {
		out = append(out, MacaroonPermissionKey(perm))
	}
	return out
}

func MacaroonHexToBase64(value string) (string, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("invalid LND macaroon response")
	}
	if len(raw) == 0 {
		return "", errors.New("empty LND macaroon response")
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func GenerateMacaroonRootKeyID(existing []uint64, now time.Time) (uint64, error) {
	if now.IsZero() {
		now = time.Now()
	}
	base := uint64(now.UTC().UnixNano() / int64(time.Millisecond))
	if base == 0 {
		base = 1
	}
	used := make(map[uint64]struct{}, len(existing))
	for _, id := range existing {
		used[id] = struct{}{}
	}
	for offset := uint64(0); offset < 1000; offset++ {
		candidate := base + offset
		if candidate == 0 {
			continue
		}
		if _, ok := used[candidate]; !ok {
			return candidate, nil
		}
	}
	return 0, errors.New("failed to generate available root key ID")
}

func ValidateMacaroonRootKeyID(rootKeyID uint64, existing []uint64) error {
	if rootKeyID == 0 {
		return errors.New("root key ID must be positive")
	}
	for _, id := range existing {
		if id == rootKeyID {
			return fmt.Errorf("root key ID %d is already in use", rootKeyID)
		}
	}
	return nil
}

func CustomMacaroonFileName(rootKeyID uint64, at time.Time) string {
	if at.IsZero() {
		at = time.Now()
	}
	return fmt.Sprintf("los-custom-macaroon-%s-rk%d.macaroon", at.UTC().Format("20060102T150405Z"), rootKeyID)
}

func normalizeMacaroonPermissionToken(value string, field string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return "", fmt.Errorf("%s required", field)
	}
	if len(normalized) > 64 {
		return "", fmt.Errorf("%s too long", field)
	}
	for _, r := range normalized {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '_' || r == '-' || r == '.' {
			continue
		}
		return "", fmt.Errorf("invalid %s", field)
	}
	return normalized, nil
}
