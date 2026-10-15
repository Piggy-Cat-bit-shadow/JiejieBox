//go:build !windows

package main

import "syscall"

const (
	priorityHigh        = 0
	priorityAboveNormal = 0
	priorityNormal      = 0
)

func configureProcess(pid int, affinity uintptr, priority uint32) error {
	return nil
}

func backgroundProcessAttributes() *syscall.SysProcAttr {
	return nil
}
