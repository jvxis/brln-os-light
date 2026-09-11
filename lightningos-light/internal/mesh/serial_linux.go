package mesh

import (
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
)

var ttyPattern = regexp.MustCompile(`^/dev/tty(USB|ACM)[0-9]+$`)

func ResolveDevice(path string) (string, error) {
	if filepath.Dir(path) != "/dev/serial/by-id" || filepath.Base(path) == "." {
		return "", errors.New("select a stable USB serial device")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if !ttyPattern.MatchString(resolved) {
		return "", errors.New("unsupported serial device")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return "", errors.New("serial device is not a character device")
	}
	return resolved, nil
}
func OpenSerial(path string) (io.ReadWriteCloser, error) {
	resolved, err := ResolveDevice(path)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(resolved, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (io.ReadWriteCloser, error) { unix.Close(fd); return nil, err }
	term, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return fail(err)
	}
	term.Iflag = 0
	term.Oflag = 0
	term.Lflag = 0
	term.Cflag = unix.B115200 | unix.CS8 | unix.CREAD | unix.CLOCAL
	term.Ispeed = unix.B115200
	term.Ospeed = unix.B115200
	term.Cc[unix.VMIN] = 1
	term.Cc[unix.VTIME] = 0
	if err = unix.IoctlSetTermios(fd, unix.TCSETS, term); err != nil {
		return fail(err)
	}
	return os.NewFile(uintptr(fd), resolved), nil
}
func PeerUID(conn net.Conn) (uint32, error) {
	c, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errors.New("Unix socket required")
	}
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid uint32
	var inner error
	err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		inner = e
		if e == nil {
			uid = cred.Uid
		}
	})
	if err != nil {
		return 0, err
	}
	return uid, inner
}
