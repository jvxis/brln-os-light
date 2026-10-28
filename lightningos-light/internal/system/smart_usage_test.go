package system

import "testing"

func TestAttachDiskUsageWithoutSmart(t *testing.T) {
	// BRLN HUB, 25/10/2026: a VPS disk smartctl cannot read. df still knows
	// the partitions; the usage must not be dropped with the SMART fields.
	usage := map[string][]DiskPartition{
		"/dev/sda": {
			{Device: "/dev/sda1", Mount: "/", TotalGB: 190, UsedGB: 95},
			{Device: "/dev/sda15", Mount: "/boot/efi", TotalGB: 0.1, UsedGB: 0.01},
		},
	}
	sizes := map[string]float64{"/dev/sda": 200}
	d := DiskSmart{Device: "/dev/sda", SmartStatus: DiskSmartUnavailable}
	attachDiskUsage(&d, "/dev/sda", usage, sizes)
	if len(d.Partitions) != 2 || d.TotalGB != 200 || d.UsedGB < 95 || d.UsedPercent < 47 || d.UsedPercent > 48 {
		t.Fatalf("usage not attached to a disk without SMART: %+v", d)
	}
	if d.SmartStatus != DiskSmartUnavailable || d.PowerOnHours != 0 || d.WearPercentUsed != 0 {
		t.Fatalf("SMART fields must stay empty: %+v", d)
	}

	// Disk with no mounted partition: the size is still known from lsblk.
	d = DiskSmart{Device: "/dev/sdb", SmartStatus: DiskSmartUnavailable}
	attachDiskUsage(&d, "/dev/sdb", usage, map[string]float64{"/dev/sdb": 50})
	if d.TotalGB != 50 || d.UsedGB != 0 || d.UsedPercent != 0 || d.Partitions != nil {
		t.Fatalf("unmounted disk keeps its size only: %+v", d)
	}

	// Without the whole-disk size, the partitions' total is used.
	d = DiskSmart{Device: "/dev/sda"}
	attachDiskUsage(&d, "/dev/sda", usage, nil)
	if d.TotalGB < 190 || d.TotalGB > 190.2 {
		t.Fatalf("partition total fallback: %+v", d)
	}
}
