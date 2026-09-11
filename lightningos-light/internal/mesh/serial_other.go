//go:build !linux

package mesh

import (
	"errors"
	"io"
	"net"
)

func ResolveDevice(string) (string, error) { return "", errors.New("LOS Mesh requires Linux") }
func OpenSerial(string) (io.ReadWriteCloser, error) {
	return nil, errors.New("LOS Mesh requires Linux")
}
func PeerUID(net.Conn) (uint32, error) { return 0, errors.New("LOS Mesh requires Linux") }
