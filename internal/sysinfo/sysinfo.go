// Package sysinfo reads what the web panel's home screen shows about the
// machine itself — CPU, memory, disk, uptime — straight from /proc and the
// filesystem, no external dependency.
package sysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Stats struct {
	CPUPercent float64
	MemUsed    uint64
	MemTotal   uint64
	DiskUsed   uint64
	DiskTotal  uint64
}

// Read samples CPU over a short window (it has no meaning as a single point)
// and reads memory/disk/uptime as they stand right now.
func Read() Stats {
	return Stats{
		CPUPercent: cpuPercent(200 * time.Millisecond),
		MemUsed:    memUsed(),
		MemTotal:   memTotal(),
		DiskUsed:   diskUsed("/"),
		DiskTotal:  diskTotal("/"),
	}
}

func cpuPercent(window time.Duration) float64 {
	idle0, total0, ok := cpuSample()
	if !ok {
		return 0
	}
	time.Sleep(window)
	idle1, total1, ok := cpuSample()
	if !ok || total1 <= total0 {
		return 0
	}
	idleDelta, totalDelta := float64(idle1-idle0), float64(total1-total0)
	pct := (1 - idleDelta/totalDelta) * 100
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// cpuSample reads the aggregate "cpu" line of /proc/stat: user, nice, system,
// idle, iowait, irq, softirq, steal — idle is field 4 (0-indexed 3), total is
// the sum of all of them.
func cpuSample() (idle, total uint64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, false
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i, s := range fields[1:] {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			continue
		}
		total += n
		if i == 3 { // idle
			idle = n
		}
	}
	return idle, total, true
}

func memTotal() uint64 { return meminfoField("MemTotal") }

func memUsed() uint64 {
	total := meminfoField("MemTotal")
	avail := meminfoField("MemAvailable")
	if avail == 0 || avail > total {
		return 0
	}
	return total - avail
}

// meminfoField reads one "Key:   12345 kB" line from /proc/meminfo, in bytes.
func meminfoField(key string) uint64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, key+":") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

func diskTotal(path string) uint64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return uint64(st.Bsize) * st.Blocks
}

func diskUsed(path string) uint64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	total := uint64(st.Bsize) * st.Blocks
	free := uint64(st.Bsize) * st.Bavail
	if free > total {
		return 0
	}
	return total - free
}
