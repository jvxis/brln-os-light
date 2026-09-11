package mesh

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"
)

func RunDaemon() {
	if len(os.Args) != 1 {
		log.Fatal("no command-line arguments accepted")
	}
	content, err := os.ReadFile("/var/lib/lightningos-mesh/device.json")
	if err != nil {
		log.Fatal("radio configuration unavailable")
	}
	var cfg struct {
		Device string `json:"device"`
	}
	if json.Unmarshal(content, &cfg) != nil {
		log.Fatal("invalid radio configuration")
	}
	manager, err := user.Lookup("lightningos")
	if err != nil {
		log.Fatal("Manager identity unavailable")
	}
	uid, err := strconv.ParseUint(manager.Uid, 10, 32)
	if err != nil {
		log.Fatal("invalid Manager identity")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	b := NewBridge(cfg.Device)
	go b.Run(ctx, func() (io.ReadWriteCloser, error) { return OpenRadio(ctx, cfg.Device) })
	if err = Serve(ctx, b, uint32(uid)); err != nil && ctx.Err() == nil {
		log.Fatal("radio bridge stopped")
	}
}
