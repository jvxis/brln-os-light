package system

import (
  "bufio"
  "bytes"
  "context"
  "os/exec"
  "path/filepath"
  "strconv"
  "strings"
)

type DiskSmart struct {
  Device string `json:"device"`
  Type string `json:"type"`
  PowerOnHours int64 `json:"power_on_hours"`
  WearPercentUsed int `json:"wear_percent_used"`
  DaysLeftEstimate int64 `json:"days_left_estimate"`
  SmartStatus string `json:"smart_status"`
  Alerts []string `json:"alerts"`
  TotalGB float64 `json:"total_gb,omitempty"`
  UsedGB float64 `json:"used_gb,omitempty"`
  UsedPercent float64 `json:"used_percent,omitempty"`
  Partitions []DiskPartition `json:"partitions,omitempty"`
}

type DiskPartition struct {
  Device string `json:"device"`
  Mount string `json:"mount,omitempty"`
  TotalGB float64 `json:"total_gb,omitempty"`
  UsedGB float64 `json:"used_gb,omitempty"`
  UsedPercent float64 `json:"used_percent,omitempty"`
}

func ReadDiskSmart(ctx context.Context) ([]DiskSmart, error) {
  devices, err := listBlockDevices(ctx)
  if err != nil {
    return nil, err
  }

  usageEntries, _ := readDiskUsageEntries(ctx)
  blockEntries, _ := readBlockDeviceEntries(ctx)
  usageByDisk := groupDiskUsage(usageEntries, blockEntries)
  diskSizes := diskSizeByPath(blockEntries)

  out := make([]DiskSmart, 0, len(devices))
  for _, dev := range devices {
    devicePath := "/dev/" + dev
    smart, err := smartctlDevice(ctx, devicePath)
    if err != nil {
      out = append(out, DiskSmart{
        Device: devicePath,
        Type: guessDiskType(devicePath),
        SmartStatus: "UNAVAILABLE",
      })
      continue
    }
    if smart.Type == "" {
      smart.Type = guessDiskType(devicePath)
    }
    if parts, ok := usageByDisk[devicePath]; ok {
      smart.Partitions = parts
      totalGB, usedGB := sumPartitionUsage(parts)
      smart.UsedGB = usedGB
      if diskTotal, ok := diskSizes[devicePath]; ok && diskTotal > 0 {
        smart.TotalGB = diskTotal
      } else if totalGB > 0 {
        smart.TotalGB = totalGB
      }
      if smart.TotalGB > 0 {
        smart.UsedPercent = (smart.UsedGB / smart.TotalGB) * 100
      }
    }
    out = append(out, smart)
  }
  return out, nil
}

func listBlockDevices(ctx context.Context) ([]string, error) {
  out, err := RunCommand(ctx, "lsblk", "-dn", "-o", "NAME,TYPE")
  if err != nil {
    return nil, err
  }
  var devices []string
  scanner := bufio.NewScanner(bytes.NewBufferString(out))
  for scanner.Scan() {
    fields := strings.Fields(scanner.Text())
    if len(fields) < 2 {
      continue
    }
    if fields[1] == "disk" {
      devices = append(devices, fields[0])
    }
  }
  return devices, nil
}

type diskUsageEntry struct {
  Device string
  Mount string
  TotalGB float64
  UsedGB float64
  UsedPercent float64
}

func readDiskUsageEntries(ctx context.Context) ([]diskUsageEntry, error) {
  out, err := RunCommand(ctx, "df", "-B1", "-x", "tmpfs", "-x", "devtmpfs")
  if err != nil {
    return nil, err
  }
  var entries []diskUsageEntry
  scanner := bufio.NewScanner(bytes.NewBufferString(out))
  first := true
  for scanner.Scan() {
    line := scanner.Text()
    if first {
      first = false
      continue
    }
    fields := strings.Fields(line)
    if len(fields) < 6 {
      continue
    }
    device := fields[0]
    if !strings.HasPrefix(device, "/dev/") {
      continue
    }
    totalBytes, _ := strconv.ParseFloat(fields[1], 64)
    usedBytes, _ := strconv.ParseFloat(fields[2], 64)
    usedPercent, _ := strconv.ParseFloat(strings.TrimSuffix(fields[4], "%"), 64)
    entries = append(entries, diskUsageEntry{
      Device: device,
      Mount: fields[5],
      TotalGB: totalBytes / (1024 * 1024 * 1024),
      UsedGB: usedBytes / (1024 * 1024 * 1024),
      UsedPercent: usedPercent,
    })
  }
  return entries, nil
}

type blockDeviceEntry struct {
  Name string
  KernelName string
  Type string
  Parent string
  Path string
  SizeBytes int64
}

func readBlockDeviceEntries(ctx context.Context) ([]blockDeviceEntry, error) {
  out, err := RunCommand(ctx, "lsblk", "-P", "-b", "-o", "NAME,KNAME,TYPE,PKNAME,PATH,SIZE")
  if err != nil {
    return nil, err
  }
  var entries []blockDeviceEntry
  scanner := bufio.NewScanner(bytes.NewBufferString(out))
  for scanner.Scan() {
    values := parseKeyValueLine(scanner.Text())
    sizeBytes, _ := strconv.ParseInt(values["SIZE"], 10, 64)
    entries = append(entries, blockDeviceEntry{
      Name: values["NAME"],
      KernelName: values["KNAME"],
      Type: values["TYPE"],
      Parent: values["PKNAME"],
      Path: values["PATH"],
      SizeBytes: sizeBytes,
    })
  }
  return entries, nil
}

func parseKeyValueLine(line string) map[string]string {
  out := make(map[string]string)
  for _, part := range strings.Fields(line) {
    pieces := strings.SplitN(part, "=", 2)
    if len(pieces) != 2 {
      continue
    }
    out[pieces[0]] = strings.Trim(pieces[1], `"`)
  }
  return out
}

func diskSizeByPath(entries []blockDeviceEntry) map[string]float64 {
  sizes := make(map[string]float64)
  for _, entry := range entries {
    if entry.Type != "disk" || entry.Path == "" || entry.SizeBytes <= 0 {
      continue
    }
    sizes[entry.Path] = float64(entry.SizeBytes) / (1024 * 1024 * 1024)
  }
  return sizes
}

func groupDiskUsage(usage []diskUsageEntry, entries []blockDeviceEntry) map[string][]DiskPartition {
  byKernel := make(map[string]blockDeviceEntry)
  byPath := make(map[string]blockDeviceEntry)
  for _, entry := range entries {
    if entry.KernelName != "" {
      byKernel[entry.KernelName] = entry
    } else if entry.Name != "" {
      byKernel[entry.Name] = entry
    }
    if entry.Path != "" {
      byPath[entry.Path] = entry
    }
    if entry.KernelName != "" {
      byPath["/dev/"+entry.KernelName] = entry
    }
  }
  grouped := make(map[string][]DiskPartition)
  for _, item := range usage {
    resolvedDevice := resolveDevicePath(item.Device)
    diskPath := rootDiskPath(resolvedDevice, byKernel, byPath)
    if diskPath == "" && resolvedDevice != item.Device {
      diskPath = rootDiskPath(item.Device, byKernel, byPath)
    }
    if diskPath == "" {
      continue
    }
    grouped[diskPath] = append(grouped[diskPath], DiskPartition{
      Device: item.Device,
      Mount: item.Mount,
      TotalGB: item.TotalGB,
      UsedGB: item.UsedGB,
      UsedPercent: item.UsedPercent,
    })
  }
  return grouped
}

func resolveDevicePath(devicePath string) string {
  if devicePath == "" || !strings.HasPrefix(devicePath, "/dev/") {
    return devicePath
  }
  resolved, err := filepath.EvalSymlinks(devicePath)
  if err != nil || resolved == "" {
    return devicePath
  }
  return resolved
}

func rootDiskPath(devicePath string, byKernel map[string]blockDeviceEntry, byPath map[string]blockDeviceEntry) string {
  if devicePath == "" {
    return ""
  }
  if entry, ok := byPath[devicePath]; ok {
    if root, ok := resolveRootDisk(entry, byKernel); ok {
      if root.Path != "" {
        return root.Path
      }
      if root.Name != "" {
        return "/dev/" + root.Name
      }
    }
  }
  name := strings.TrimPrefix(devicePath, "/dev/")
  if entry, ok := byKernel[name]; ok {
    if root, ok := resolveRootDisk(entry, byKernel); ok {
      if root.Path != "" {
        return root.Path
      }
      if root.Name != "" {
        return "/dev/" + root.Name
      }
    }
  }
  if strings.HasPrefix(devicePath, "/dev/") {
    return baseDiskPath(devicePath)
  }
  return ""
}

func resolveRootDisk(entry blockDeviceEntry, byKernel map[string]blockDeviceEntry) (blockDeviceEntry, bool) {
  current := entry
  for i := 0; i < 12; i++ {
    if current.Type == "disk" {
      return current, true
    }
    if current.Parent == "" {
      break
    }
    parent, ok := byKernel[current.Parent]
    if !ok {
      break
    }
    current = parent
  }
  return blockDeviceEntry{}, false
}

func baseDiskPath(devicePath string) string {
  name := strings.TrimPrefix(devicePath, "/dev/")
  switch {
  case strings.HasPrefix(name, "nvme") && strings.Contains(name, "p"):
    if idx := strings.LastIndex(name, "p"); idx > 0 {
      return "/dev/" + name[:idx]
    }
  case strings.HasPrefix(name, "mmcblk") && strings.Contains(name, "p"):
    if idx := strings.LastIndex(name, "p"); idx > 0 {
      return "/dev/" + name[:idx]
    }
  default:
    for i := len(name) - 1; i >= 0; i-- {
      if name[i] < '0' || name[i] > '9' {
        return "/dev/" + name[:i+1]
      }
    }
  }
  return devicePath
}

func sumPartitionUsage(parts []DiskPartition) (float64, float64) {
  var total float64
  var used float64
  for _, part := range parts {
    if part.TotalGB <= 0 || part.UsedGB < 0 {
      continue
    }
    total += part.TotalGB
    used += part.UsedGB
  }
  return total, used
}

func smartctlDevice(ctx context.Context, device string) (DiskSmart, error) {
  out, err := RunCommandWithSudo(ctx, smartctlPath(), "-a", device)
  if err != nil && strings.TrimSpace(out) == "" {
    return DiskSmart{}, err
  }

  smart := DiskSmart{
    Device: device,
    SmartStatus: "UNKNOWN",
    Type: guessDiskType(device),
  }

  lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
  for _, line := range lines {
    lower := strings.ToLower(strings.TrimSpace(line))
    if strings.Contains(lower, "smart support is:") {
      if strings.Contains(lower, "unavailable") {
        smart.SmartStatus = "UNAVAILABLE"
      } else if strings.Contains(lower, "disabled") {
        smart.SmartStatus = "DISABLED"
      }
    }
    if strings.Contains(line, "SMART overall-health self-assessment test result") {
      if strings.Contains(line, "PASSED") {
        smart.SmartStatus = "PASSED"
      } else {
        smart.SmartStatus = "FAILED"
      }
    }
    if strings.Contains(line, "SMART Health Status:") {
      if strings.Contains(line, "OK") {
        smart.SmartStatus = "PASSED"
      } else {
        smart.SmartStatus = "FAILED"
      }
    }

    if strings.Contains(line, "Percentage Used") {
      smart.Type = "NVMe"
      smart.WearPercentUsed = parseSmartInt(line)
    }
    if strings.Contains(line, "Power On Hours") {
      smart.PowerOnHours = parseSmartInt64(line)
    }
    if strings.HasPrefix(strings.TrimSpace(line), "Power_On_Hours") {
      smart.PowerOnHours = parseSmartAttribute(line)
    }
  }

  smart = addSmartAlerts(smart)
  return smart, nil
}

func smartctlPath() string {
  if path, err := exec.LookPath("smartctl"); err == nil {
    return path
  }
  return "smartctl"
}

func guessDiskType(device string) string {
  name := strings.TrimPrefix(device, "/dev/")
  if strings.HasPrefix(name, "nvme") {
    return "NVMe"
  }
  if strings.HasPrefix(name, "mmcblk") {
    return "MMC"
  }
  return "Disk"
}

func parseSmartInt(line string) int {
  parts := strings.Fields(line)
  for i := len(parts) - 1; i >= 0; i-- {
    if v, err := strconv.Atoi(strings.TrimSpace(parts[i])); err == nil {
      return v
    }
  }
  return 0
}

func parseSmartInt64(line string) int64 {
  parts := strings.Fields(line)
  for i := len(parts) - 1; i >= 0; i-- {
    if v, err := strconv.ParseInt(strings.TrimSpace(parts[i]), 10, 64); err == nil {
      return v
    }
  }
  return 0
}

func parseSmartAttribute(line string) int64 {
  parts := strings.Fields(line)
  if len(parts) < 10 {
    return 0
  }
  v, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
  return v
}

func addSmartAlerts(s DiskSmart) DiskSmart {
  if s.WearPercentUsed >= 90 {
    s.Alerts = append(s.Alerts, "wear_err")
  } else if s.WearPercentUsed >= 70 {
    s.Alerts = append(s.Alerts, "wear_warn")
  }

  if s.SmartStatus == "FAILED" {
    s.Alerts = append(s.Alerts, "smart_failed")
  }

  if s.WearPercentUsed > 0 && s.PowerOnHours > 0 {
    wearRate := float64(s.WearPercentUsed) / float64(s.PowerOnHours)
    if wearRate > 0 {
      hoursLeft := (100.0 - float64(s.WearPercentUsed)) / wearRate
      s.DaysLeftEstimate = int64(hoursLeft / 24.0)
    }
  }

  return s
}
