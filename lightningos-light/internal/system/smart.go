package system

import (
  "bufio"
  "bytes"
  "context"
  "os/exec"
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
}

func ReadDiskSmart(ctx context.Context) ([]DiskSmart, error) {
  devices, err := listBlockDevices(ctx)
  if err != nil {
    return nil, err
  }

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
