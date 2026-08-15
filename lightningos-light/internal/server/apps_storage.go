package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
)

const (
	storageLayoutRoot = "lightningos"
)

type appStorageTarget struct {
	Mount         string  `json:"mount"`
	Source        string  `json:"source,omitempty"`
	FSType        string  `json:"fstype,omitempty"`
	TotalGB       float64 `json:"total_gb,omitempty"`
	UsedGB        float64 `json:"used_gb,omitempty"`
	FreeGB        float64 `json:"free_gb,omitempty"`
	UsedPercent   float64 `json:"used_percent,omitempty"`
	Eligible      bool    `json:"eligible"`
	Reason        string  `json:"reason,omitempty"`
	SuggestedPath string  `json:"suggested_path"`
}

type appStorageTargetsResponse struct {
	App         string             `json:"app"`
	DefaultPath string             `json:"default_path"`
	MinFreeGB   float64            `json:"min_free_gb"`
	Targets     []appStorageTarget `json:"targets"`
}

type storageMount struct {
	Mount     string
	Source    string
	FSType    string
	Options   string
	SizeBytes int64
	UsedBytes int64
	FreeBytes int64
}

type findmntOutput struct {
	Filesystems []findmntFilesystem `json:"filesystems"`
}

type findmntFilesystem struct {
	Target   string              `json:"target"`
	Source   string              `json:"source"`
	FSType   string              `json:"fstype"`
	Options  string              `json:"options"`
	Size     json.RawMessage     `json:"size"`
	Used     json.RawMessage     `json:"used"`
	Avail    json.RawMessage     `json:"avail"`
	Children []findmntFilesystem `json:"children"`
}

func (s *Server) handleAppStorageTargets(w http.ResponseWriter, r *http.Request) {
	appID := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("app")))
	if !appSupportsStorageTargets(appID) {
		writeError(w, http.StatusBadRequest, "storage targets are not supported for this app")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp, err := appStorageTargets(ctx, appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func appStorageTargets(ctx context.Context, appID string) (appStorageTargetsResponse, error) {
	if !appSupportsStorageTargets(appID) {
		return appStorageTargetsResponse{}, errors.New("storage targets are not supported for this app")
	}
	mounts, err := readStorageMounts(ctx)
	if err != nil {
		return appStorageTargetsResponse{}, fmt.Errorf("failed to list mounted volumes: %w", err)
	}
	targets := make([]appStorageTarget, 0, len(mounts))
	for _, mount := range mounts {
		targets = append(targets, buildAppStorageTarget(appID, mount))
	}
	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].Eligible != targets[j].Eligible {
			return targets[i].Eligible
		}
		if targets[i].FreeGB != targets[j].FreeGB {
			return targets[i].FreeGB > targets[j].FreeGB
		}
		return targets[i].Mount < targets[j].Mount
	})
	return appStorageTargetsResponse{
		App:         appID,
		DefaultPath: appDefaultDataDir(appID),
		MinFreeGB:   kibToGB(appMinFreeKiB(appID)),
		Targets:     targets,
	}, nil
}

func resolveInstallDataDirFromStorageMount(ctx context.Context, appID string, mount string) (string, error) {
	normalizedMount, err := normalizeStorageMount(mount)
	if err != nil {
		return "", err
	}
	resp, err := appStorageTargets(ctx, appID)
	if err != nil {
		return "", err
	}
	for _, target := range resp.Targets {
		if target.Mount != normalizedMount {
			continue
		}
		if !target.Eligible {
			if target.Reason != "" {
				return "", fmt.Errorf("selected storage volume is not eligible: %s", target.Reason)
			}
			return "", errors.New("selected storage volume is not eligible")
		}
		return target.SuggestedPath, nil
	}
	return "", errors.New("selected storage volume is not mounted")
}

func readStorageMounts(ctx context.Context) ([]storageMount, error) {
	out, err := system.RunCommand(ctx, "findmnt", "-J", "-b", "-o", "TARGET,SOURCE,FSTYPE,OPTIONS,SIZE,USED,AVAIL")
	if err != nil {
		return nil, err
	}
	var parsed findmntOutput
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil, err
	}
	mounts := []storageMount{}
	hostOptions := readHostMountOptions("/proc/1/mountinfo")
	indexByMount := map[string]int{}
	var walk func([]findmntFilesystem)
	walk = func(items []findmntFilesystem) {
		for _, item := range items {
			candidate := storageMountFromFindmnt(item)
			if options, ok := hostOptions[candidate.Mount]; ok {
				candidate.Options = options
			}
			if candidate.Mount != "" {
				if idx, ok := indexByMount[candidate.Mount]; ok {
					if preferStorageMount(candidate, mounts[idx]) {
						mounts[idx] = candidate
					}
				} else {
					indexByMount[candidate.Mount] = len(mounts)
					mounts = append(mounts, candidate)
				}
			}
			if len(item.Children) > 0 {
				walk(item.Children)
			}
		}
	}
	walk(parsed.Filesystems)
	return mounts, nil
}

func readHostMountOptions(filename string) map[string]string {
	options := map[string]string{}
	raw, err := os.ReadFile(filename)
	if err != nil {
		return options
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		mount := decodeMountInfoPath(fields[4])
		if !strings.HasPrefix(mount, "/") {
			continue
		}
		options[path.Clean(mount)] = fields[5]
	}
	return options
}

func decodeMountInfoPath(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}

func storageMountFromFindmnt(item findmntFilesystem) storageMount {
	mount := strings.TrimSpace(item.Target)
	if mount == "" {
		return storageMount{}
	}
	return storageMount{
		Mount:     path.Clean(mount),
		Source:    strings.TrimSpace(item.Source),
		FSType:    strings.TrimSpace(item.FSType),
		Options:   strings.TrimSpace(item.Options),
		SizeBytes: findmntSizeBytes(item.Size),
		UsedBytes: findmntSizeBytes(item.Used),
		FreeBytes: findmntSizeBytes(item.Avail),
	}
}

func preferStorageMount(candidate storageMount, current storageMount) bool {
	candidateReal := candidate.FSType != "" && !storagePseudoFilesystem(candidate.FSType)
	currentReal := current.FSType != "" && !storagePseudoFilesystem(current.FSType)
	if candidateReal != currentReal {
		return candidateReal
	}
	candidateSupported := storageSupportedFilesystem(candidate.FSType)
	currentSupported := storageSupportedFilesystem(current.FSType)
	if candidateSupported != currentSupported {
		return candidateSupported
	}
	if candidate.FreeBytes > 0 && current.FreeBytes <= 0 {
		return true
	}
	if candidate.SizeBytes > 0 && current.SizeBytes <= 0 {
		return true
	}
	if strings.HasPrefix(candidate.Source, "/dev/") && !strings.HasPrefix(current.Source, "/dev/") {
		return true
	}
	return false
}

func buildAppStorageTarget(appID string, mount storageMount) appStorageTarget {
	target := appStorageTarget{
		Mount:         path.Clean(strings.TrimSpace(mount.Mount)),
		Source:        strings.TrimSpace(mount.Source),
		FSType:        strings.TrimSpace(mount.FSType),
		TotalGB:       bytesToGB(mount.SizeBytes),
		UsedGB:        bytesToGB(mount.UsedBytes),
		FreeGB:        bytesToGB(mount.FreeBytes),
		SuggestedPath: suggestedStorageDataDir(appID, mount.Mount),
	}
	if mount.SizeBytes > 0 && mount.UsedBytes >= 0 {
		target.UsedPercent = (float64(mount.UsedBytes) / float64(mount.SizeBytes)) * 100
	}
	target.Eligible, target.Reason = storageMountEligibility(appID, mount, target.SuggestedPath)
	return target
}

func storageMountEligibility(appID string, mount storageMount, suggestedPath string) (bool, string) {
	normalizedMount, err := normalizeStorageMount(mount.Mount)
	if err != nil {
		return false, err.Error()
	}
	if normalizedMount == "/" {
		return false, "root filesystem is not selectable"
	}
	if storageMountBlocked(normalizedMount) {
		return false, "system mount is not selectable"
	}
	if storageMountReadOnly(mount.Options) {
		return false, "volume is mounted read-only"
	}
	fstype := strings.ToLower(strings.TrimSpace(mount.FSType))
	if fstype == "" {
		return false, "filesystem type unavailable"
	}
	if storagePseudoFilesystem(fstype) {
		return false, "not a persistent data filesystem"
	}
	if !storageSupportedFilesystem(fstype) {
		return false, "filesystem must support Linux ownership and permissions"
	}
	if mount.FreeBytes <= 0 {
		return false, "free space unavailable"
	}
	minBytes := appMinFreeKiB(appID) * 1024
	if minBytes > 0 && mount.FreeBytes < minBytes {
		return false, "not enough free space"
	}
	switch appID {
	case bitcoinCoreAppID:
		if _, err := normalizeBitcoinCoreDataDir(suggestedPath); err != nil {
			return false, err.Error()
		}
	case elementsAppID:
		if _, err := normalizeElementsDataDir(suggestedPath); err != nil {
			return false, err.Error()
		}
	case electrsAppID, mempoolAppID:
		if _, err := appmanifest.NormalizeCatalogDataDir(appID, suggestedPath); err != nil {
			return false, err.Error()
		}
	default:
		return false, "unsupported app"
	}
	return true, ""
}

func normalizeStorageMount(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("storage mount is required")
	}
	if strings.Contains(trimmed, "\\") {
		return "", errors.New("storage mount must be a Linux absolute path")
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", errors.New("storage mount must be an absolute path")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." {
		return "", errors.New("storage mount must be an absolute path")
	}
	if !linuxPathHasSafeChars(cleaned) {
		return "", errors.New("storage mount may only contain letters, numbers, slash, dot, underscore, and hyphen")
	}
	return cleaned, nil
}

func suggestedStorageDataDir(appID string, mount string) string {
	normalized, err := normalizeStorageMount(mount)
	if err != nil {
		normalized = path.Clean(strings.TrimSpace(mount))
	}
	return path.Join(normalized, storageLayoutRoot, appStorageDirName(appID))
}

func appSupportsStorageTargets(appID string) bool {
	return appID == bitcoinCoreAppID || appID == elementsAppID || appID == electrsAppID || appID == mempoolAppID
}

func appStorageDirName(appID string) string {
	switch appID {
	case bitcoinCoreAppID:
		return "bitcoin"
	case elementsAppID:
		return "elements"
	default:
		return appID
	}
}

func appDefaultDataDir(appID string) string {
	switch appID {
	case bitcoinCoreAppID:
		return bitcoinCoreDefaultDataDir
	case elementsAppID:
		return elementsDefaultDataDir
	case electrsAppID:
		return appmanifest.ElectrsDefaultDataDir
	case mempoolAppID:
		return appmanifest.MempoolDefaultDataDir
	default:
		return ""
	}
}

func appMinFreeKiB(appID string) int64 {
	switch appID {
	case bitcoinCoreAppID:
		return bitcoinCoreMinFreeKiB
	case elementsAppID:
		return elementsMinFreeKiB
	case electrsAppID:
		return 100 * 1024 * 1024
	case mempoolAppID:
		return 20 * 1024 * 1024
	default:
		return 0
	}
}

func storageMountBlocked(mount string) bool {
	blocked := []string{
		"/bin",
		"/boot",
		"/dev",
		"/etc",
		"/lib",
		"/lib64",
		"/proc",
		"/root",
		"/run",
		"/sbin",
		"/snap",
		"/sys",
		"/tmp",
		"/usr",
		"/var/lib/containerd",
		"/var/lib/docker",
	}
	for _, prefix := range blocked {
		if mount == prefix || strings.HasPrefix(mount, prefix+"/") {
			return true
		}
	}
	return false
}

func storageMountReadOnly(options string) bool {
	for _, option := range strings.Split(options, ",") {
		if strings.TrimSpace(option) == "ro" {
			return true
		}
	}
	return false
}

func storagePseudoFilesystem(fstype string) bool {
	switch strings.ToLower(strings.TrimSpace(fstype)) {
	case "autofs", "bpf", "cgroup", "cgroup2", "configfs", "debugfs", "devpts", "devtmpfs",
		"efivarfs", "fusectl", "hugetlbfs", "mqueue", "nsfs", "overlay", "proc", "pstore",
		"ramfs", "rpc_pipefs", "securityfs", "squashfs", "sysfs", "tmpfs", "tracefs":
		return true
	default:
		return false
	}
}

func storageSupportedFilesystem(fstype string) bool {
	switch strings.ToLower(strings.TrimSpace(fstype)) {
	case "bcachefs", "btrfs", "ext2", "ext3", "ext4", "f2fs", "nilfs2", "xfs", "zfs":
		return true
	default:
		return false
	}
}

func findmntSizeBytes(raw json.RawMessage) int64 {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return 0
	}
	if strings.HasPrefix(value, `"`) {
		var decoded string
		if err := json.Unmarshal(raw, &decoded); err == nil {
			value = strings.TrimSpace(decoded)
		}
	}
	if value == "" {
		return 0
	}
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		return parsed
	}
	if parsed, err := strconv.ParseFloat(value, 64); err == nil {
		return int64(parsed)
	}
	return 0
}

func bytesToGB(value int64) float64 {
	if value <= 0 {
		return 0
	}
	gb := float64(value) / (1024 * 1024 * 1024)
	return math.Round(gb*10) / 10
}

func kibToGB(value int64) float64 {
	if value <= 0 {
		return 0
	}
	gb := float64(value*1024) / (1024 * 1024 * 1024)
	return math.Round(gb*10) / 10
}
