//go:build windows

package app

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: a console program started by the app
// gets no console of its own, so ffmpeg's window never flashes over the
// picture.
const createNoWindow = 0x08000000

// hideWindow keeps a program the app starts out of sight, which a console
// program would otherwise show as a window of its own.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
