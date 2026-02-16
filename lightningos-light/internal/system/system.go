package system

import (
  "bufio"
  "bytes"
  "context"
  "errors"
  "fmt"
  "os"
  "os/exec"
  "strconv"
  "strings"
  "time"
)

type DiskUsage struct {
  Mount string  `json:"mount"`
  TotalGB float64 `json:"total_gb"`
  UsedGB  float64 `json:"used_gb"`
  UsedPercent float64 `json:"used_percent"`
}

type SystemStats struct {
  UptimeSec int64 `json:"uptime_sec"`
  CPULoad1 float64 `json:"cpu_load_1"`
  CPUPercent float64 `json:"cpu_percent"`
  RAMTotalMB int64 `json:"ram_total_mb"`
  RAMUsedMB int64 `json:"ram_used_mb"`
  Disk []DiskUsage `json:"disk"`
  TemperatureC float64 `json:"temperature_c"`
}

func GetSystemStats(ctx context.Context) (SystemStats, error) {
  var stats SystemStats

  uptime, err := readUptime()
  if err != nil {
    return stats, err
  }
  stats.UptimeSec = uptime

  load1, err := readLoad1()
  if err == nil {
    stats.CPULoad1 = load1
  }

  cpuPercent, err := readCPUPercent(120 * time.Millisecond)
  if err == nil {
    stats.CPUPercent = cpuPercent
  }

  totalMB, usedMB, err := readMemInfo()
  if err == nil {
    stats.RAMTotalMB = totalMB
    stats.RAMUsedMB = usedMB
  }

  disks, err := readDiskUsage(ctx)
  if err == nil {
    stats.Disk = disks
  }

  tempC, err := readTemperatureC()
  if err == nil {
    stats.TemperatureC = tempC
  }

  return stats, nil
}

func readUptime() (int64, error) {
  b, err := os.ReadFile("/proc/uptime")
  if err != nil {
    return 0, err
  }
  parts := strings.Fields(string(b))
  if len(parts) == 0 {
    return 0, errors.New("uptime parse error")
  }
  seconds, err := strconv.ParseFloat(parts[0], 64)
  if err != nil {
    return 0, err
  }
  return int64(seconds), nil
}

func readLoad1() (float64, error) {
  b, err := os.ReadFile("/proc/loadavg")
  if err != nil {
    return 0, err
  }
  parts := strings.Fields(string(b))
  if len(parts) < 1 {
    return 0, errors.New("loadavg parse error")
  }
  return strconv.ParseFloat(parts[0], 64)
}

func readCPUPercent(delay time.Duration) (float64, error) {
  idle1, total1, err := readCPUStat()
  if err != nil {
    return 0, err
  }
  time.Sleep(delay)
  idle2, total2, err := readCPUStat()
  if err != nil {
    return 0, err
  }
  idle := idle2 - idle1
  total := total2 - total1
  if total == 0 {
    return 0, errors.New("cpu total zero")
  }
  usage := (1.0 - float64(idle)/float64(total)) * 100.0
  return usage, nil
}

func readCPUStat() (idle uint64, total uint64, err error) {
  b, err := os.ReadFile("/proc/stat")
  if err != nil {
    return 0, 0, err
  }
  scanner := bufio.NewScanner(bytes.NewReader(b))
  if !scanner.Scan() {
    return 0, 0, errors.New("cpu stat empty")
  }
  fields := strings.Fields(scanner.Text())
  if len(fields) < 5 {
    return 0, 0, errors.New("cpu stat parse error")
  }
  for i := 1; i < len(fields); i++ {
    v, err := strconv.ParseUint(fields[i], 10, 64)
    if err != nil {
      continue
    }
    total += v
    if i == 4 {
      idle = v
    }
  }
  return idle, total, nil
}

func readMemInfo() (totalMB int64, usedMB int64, err error) {
  b, err := os.ReadFile("/proc/meminfo")
  if err != nil {
    return 0, 0, err
  }
  var totalKB, freeKB, buffersKB, cachedKB int64
  scanner := bufio.NewScanner(bytes.NewReader(b))
  for scanner.Scan() {
    line := scanner.Text()
    if strings.HasPrefix(line, "MemTotal:") {
      totalKB = parseMemValue(line)
    }
    if strings.HasPrefix(line, "MemFree:") {
      freeKB = parseMemValue(line)
    }
    if strings.HasPrefix(line, "Buffers:") {
      buffersKB = parseMemValue(line)
    }
    if strings.HasPrefix(line, "Cached:") {
      cachedKB = parseMemValue(line)
    }
  }

  if totalKB == 0 {
    return 0, 0, errors.New("meminfo missing")
  }
  usedKB := totalKB - freeKB - buffersKB - cachedKB
  return totalKB / 1024, usedKB / 1024, nil
}

func parseMemValue(line string) int64 {
  parts := strings.Fields(line)
  if len(parts) < 2 {
    return 0
  }
  v, _ := strconv.ParseInt(parts[1], 10, 64)
  return v
}

func readDiskUsage(ctx context.Context) ([]DiskUsage, error) {
  cmd := exec.CommandContext(ctx, "df", "-B1", "-x", "tmpfs", "-x", "devtmpfs")
  out, err := cmd.Output()
  if err != nil {
    return nil, err
  }

  var disks []DiskUsage
  scanner := bufio.NewScanner(bytes.NewReader(out))
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
    totalBytes, _ := strconv.ParseFloat(fields[1], 64)
    usedBytes, _ := strconv.ParseFloat(fields[2], 64)
    usedPercent, _ := strconv.ParseFloat(strings.TrimSuffix(fields[4], "%"), 64)
    disks = append(disks, DiskUsage{
      Mount: fields[5],
      TotalGB: totalBytes / (1024 * 1024 * 1024),
      UsedGB: usedBytes / (1024 * 1024 * 1024),
      UsedPercent: usedPercent,
    })
  }
  return disks, nil
}

func readTemperatureC() (float64, error) {
  data, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
  if err != nil {
    return 0, err
  }
  v, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
  if err != nil {
    return 0, err
  }
  if v > 1000 {
    v = v / 1000.0
  }
  return v, nil
}

func RunCommand(ctx context.Context, name string, args ...string) (string, error) {
  cmd := exec.CommandContext(ctx, name, args...)
  out, err := cmd.CombinedOutput()
  if err != nil {
    return string(out), fmt.Errorf("%s failed: %w", name, err)
  }
  return string(out), nil
}

func RunCommandWithSudo(ctx context.Context, name string, args ...string) (string, error) {
  out, err := RunCommand(ctx, name, args...)
  if err == nil {
    return out, nil
  }
  sudoPath, sudoErr := exec.LookPath("sudo")
  if sudoErr != nil {
    return out, err
  }
  sudoArgs := append([]string{"-n", name}, args...)
  sudoOut, sudoErr := RunCommand(ctx, sudoPath, sudoArgs...)
  if sudoErr == nil {
    return sudoOut, nil
  }
  return sudoOut, fmt.Errorf("%s failed: %w; sudo failed: %v", name, err, sudoErr)
}

func systemctlPath() string {
  if path, err := exec.LookPath("systemctl"); err == nil {
    return path
  }
  return "systemctl"
}

func SystemctlIsActive(ctx context.Context, service string) bool {
  out, err := RunCommand(ctx, systemctlPath(), "is-active", service)
  if err != nil {
    return false
  }
  return strings.TrimSpace(out) == "active"
}

func SystemctlRestart(ctx context.Context, service string) error {
  systemctl := systemctlPath()
  _, err := RunCommand(ctx, systemctl, "restart", service)
  if err == nil {
    return nil
  }
  sudoPath, sudoErr := exec.LookPath("sudo")
  if sudoErr != nil {
    return err
  }
  if _, sudoErr = RunCommand(ctx, sudoPath, "-n", systemctl, "restart", service); sudoErr == nil {
    return nil
  }
  return fmt.Errorf("systemctl restart failed: %w; sudo restart failed: %v", err, sudoErr)
}

func SystemctlPower(ctx context.Context, action string) error {
  if action != "reboot" && action != "poweroff" {
    return fmt.Errorf("unsupported system action")
  }
  systemctl := systemctlPath()
  if _, err := RunCommandWithSudo(ctx, systemctl, action); err != nil {
    return fmt.Errorf("systemctl %s failed: %w", action, err)
  }
  return nil
}

func JournalTail(ctx context.Context, service string, lines int) ([]string, error) {
  return JournalTailSince(ctx, service, lines, "")
}

func JournalTailSince(ctx context.Context, service string, lines int, since string) ([]string, error) {
  if lines <= 0 {
    lines = 200
  }
  args := []string{"-u", service, "-n", strconv.Itoa(lines), "--no-pager"}
  if strings.TrimSpace(since) != "" {
    args = append(args, "--since", since)
  }
  out, err := RunCommand(ctx, "journalctl", args...)
  if err != nil {
    return nil, err
  }
  raw := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
  var trimmed []string
  for _, line := range raw {
    if strings.TrimSpace(line) == "" {
      continue
    }
    trimmed = append(trimmed, line)
  }
  return trimmed, nil
}
