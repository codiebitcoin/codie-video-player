//go:build !windows

package app

import "os/exec"

// hideWindow does nothing outside Windows, where a program the app starts
// opens no window of its own.
func hideWindow(*exec.Cmd) {}
