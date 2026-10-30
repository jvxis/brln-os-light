package server

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

const kernelTCPStatusPath = "/run/lightningos-kernel-tcp/status.json"

type kernelTCPStatus struct {
	Version        int   `json:"version"`
	CheckedAt      int64 `json:"checked_at"`
	Disabled       bool  `json:"disabled"`
	FilterVerified bool  `json:"filter_verified"`
	RepairCount    int64 `json:"repair_count"`
}

func parseKernelTCPStatus(reader io.Reader, now time.Time) (kernelTCPStatus, bool) {
	var status kernelTCPStatus
	decoder := json.NewDecoder(io.LimitReader(reader, 4097))
	if decoder.Decode(&status) != nil || status.Version != 1 || status.RepairCount < 0 {
		return kernelTCPStatus{}, false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || status.CheckedAt > now.Unix()+5 || status.CheckedAt < now.Unix()-90 {
		return kernelTCPStatus{}, false
	}
	return status, true
}

func readKernelTCPStatus() (kernelTCPStatus, bool) {
	// Both the parent and file are root-owned and unwritable by the Manager.
	for _, path := range []string{filepath.Dir(kernelTCPStatusPath), kernelTCPStatusPath} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || !kernelTCPRootOwned(info) {
			return kernelTCPStatus{}, false
		}
		if path == kernelTCPStatusPath && (!info.Mode().IsRegular() || info.Size() > 4096) {
			return kernelTCPStatus{}, false
		}
	}
	file, err := os.Open(kernelTCPStatusPath)
	if err != nil {
		return kernelTCPStatus{}, false
	}
	defer file.Close()
	return parseKernelTCPStatus(file, time.Now())
}

func kernelTCPSystemCheckItem(status kernelTCPStatus, available bool) systemCheckItem {
	item := systemCheckItem{ID: "kernel_tcp", Label: "IPv4 TCP options mitigation", Status: systemCheckWarn,
		Detail: "CVE-2026-98432: current kernel filter could not be verified"}
	if !available {
		return item
	}
	item.Value = status.FilterVerified && !status.Disabled
	if status.Disabled {
		item.Detail = "CVE-2026-98432: mitigation explicitly disabled by the operator"
	} else if status.FilterVerified {
		item.Status = systemCheckOK
		item.Detail = "CVE-2026-98432: IPv4 TCP options filter verified; temporary mitigation, not proof of a patched kernel"
	}
	return item
}

func (s *Server) startKernelTCPAuditMonitor() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		last := ""
		var lastRepairs int64
		for {
			status, available := readKernelTCPStatus()
			// Timestamp changes are not audit events. State/repair changes are.
			metadata := struct {
				Available bool  `json:"available"`
				Verified  bool  `json:"filter_verified"`
				Disabled  bool  `json:"disabled"`
				Repairs   int64 `json:"repair_count"`
			}{available, status.FilterVerified, status.Disabled, status.RepairCount}
			raw, _ := json.Marshal(metadata)
			if string(raw) != last {
				if service, _ := s.auditLogService(); service != nil {
					action := "security.kernel_tcp.observed"
					if !available || !status.FilterVerified || status.Disabled {
						action = "security.kernel_tcp.unverified"
					} else if last != "" && status.RepairCount > lastRepairs {
						action = "security.kernel_tcp.repaired"
					}
					ctx, cancel := context.WithTimeout(s.shutdownContext(), auditInsertTimeout)
					if err := service.Insert(ctx, AuditEventInsert{SessionID: "system", Action: action,
						Target: "CVE-2026-98432", Metadata: metadata}); err == nil {
						last = string(raw)
						lastRepairs = status.RepairCount
					}
					cancel()
				}
			}
			select {
			case <-s.shutdownContext().Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
