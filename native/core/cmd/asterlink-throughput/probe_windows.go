package main

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const defaultCoreName = "asterlink-core.exe"

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procGetProcessInformation = kernel32.NewProc("GetProcessInformation")
	procGetSystemPowerStatus  = kernel32.NewProc("GetSystemPowerStatus")
)

// The same constants as apps/windows/AsterLink.Platform/Native.cs (handoff_fix.md F4, D8).
const (
	processPowerThrottling        = 4
	powerThrottlingVersion        = 1
	powerThrottlingExecutionSpeed = 0x1
)

type powerThrottlingState struct{ Version, ControlMask, StateMask uint32 }

type systemPowerStatus struct {
	ACLineStatus, BatteryFlag, BatteryLifePercent, SystemStatusFlag uint8
	BatteryLifeTime, BatteryFullLifeTime                            uint32
}

func findCore(name string) (pid, threads int) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, 0
	}
	defer windows.CloseHandle(snap)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), name) {
			return int(entry.ProcessID), int(entry.Threads)
		}
	}
	return 0, 0
}

func threadCount(pid int) int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		if int(entry.ProcessID) == pid {
			return int(entry.Threads)
		}
	}
	return 0
}

func priorityClassName(class uint32) string {
	switch class {
	case windows.IDLE_PRIORITY_CLASS:
		return "idle"
	case windows.BELOW_NORMAL_PRIORITY_CLASS:
		return "below_normal"
	case windows.NORMAL_PRIORITY_CLASS:
		return "normal"
	case windows.ABOVE_NORMAL_PRIORITY_CLASS:
		return "above_normal"
	case windows.HIGH_PRIORITY_CLASS:
		return "high"
	case windows.REALTIME_PRIORITY_CLASS:
		return "realtime"
	}
	return fmt.Sprintf("0x%x", class)
}

// throttlingName: "system" = the system's heuristics decide (a windowless child of a service
// then gets Utility QoS on battery, the F4 case); "opted_out" = execution speed is managed and
// not throttled (High QoS, what the service sets since round 1); "eco" = EcoQoS.
func throttlingName(s powerThrottlingState) string {
	switch {
	case s.ControlMask&powerThrottlingExecutionSpeed == 0:
		return "system"
	case s.StateMask&powerThrottlingExecutionSpeed == 0:
		return "opted_out"
	}
	return "eco"
}

func readThrottling(h windows.Handle) (string, error) {
	state := powerThrottlingState{Version: powerThrottlingVersion}
	ok, _, err := procGetProcessInformation.Call(uintptr(h), processPowerThrottling, uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
	if ok == 0 {
		return "", err
	}
	return throttlingName(state), nil
}

func powerSource() string {
	var s systemPowerStatus
	if ok, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s))); ok == 0 {
		return ""
	}
	switch s.ACLineStatus {
	case 0:
		return "battery"
	case 1:
		return "ac"
	}
	return "unknown"
}

// probeCore opens the Core for query only. The Core runs as LocalSystem; a standard user can
// usually read the priority class but may be refused the throttling policy, which is then
// reported as an error (run the tool elevated to read it). Nothing is ever changed.
func probeCore(name string, pid int) coreSample {
	sample := coreSample{Power: powerSource()}
	if pid == 0 {
		pid, sample.Threads = findCore(name)
	} else {
		sample.Threads = threadCount(pid)
	}
	if pid == 0 {
		sample.Err = "not running"
		return sample
	}
	sample.PID = pid
	var errs []string
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		h, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	}
	if err != nil {
		sample.Err = "open: " + err.Error()
		return sample
	}
	defer windows.CloseHandle(h)
	if class, err := windows.GetPriorityClass(h); err == nil {
		sample.PrioClass = priorityClassName(class)
	} else {
		errs = append(errs, "priority class: "+err.Error())
	}
	if t, err := readThrottling(h); err == nil {
		sample.Throttling = t
	} else {
		errs = append(errs, "throttling: "+err.Error())
	}
	sample.Err = strings.Join(errs, "; ")
	return sample
}
