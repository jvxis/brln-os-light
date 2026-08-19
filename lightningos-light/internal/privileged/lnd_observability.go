package privileged

import (
	"context"
	"errors"
	"os"
)

const defaultLNDChannelDBPath = "/data/lnd/data/graph/mainnet/channel.db"

type NativeLNDObservabilityManager struct {
	channelDBPath string
}

func NewNativeLNDObservabilityManager() *NativeLNDObservabilityManager {
	return &NativeLNDObservabilityManager{channelDBPath: defaultLNDChannelDBPath}
}

func (manager *NativeLNDObservabilityManager) ChannelDBSize(ctx context.Context) (LNDChannelDBState, error) {
	select {
	case <-ctx.Done():
		return LNDChannelDBState{}, ctx.Err()
	default:
	}

	path := defaultLNDChannelDBPath
	if manager != nil && manager.channelDBPath != "" {
		path = manager.channelDBPath
	}
	info, err := os.Lstat(path)
	if err != nil {
		return LNDChannelDBState{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return LNDChannelDBState{}, errors.New("LND channel database is not a regular file")
	}
	if info.Size() < 0 {
		return LNDChannelDBState{}, errors.New("LND channel database size is invalid")
	}
	return LNDChannelDBState{SizeBytes: info.Size()}, nil
}
