// Package priv runs the few commands mor cannot do on its own.
//
// The daemon serves a web panel to the internet, so it does not run as root:
// a hole in the panel then costs the panel, not the machine. What it still
// needs is to restart the two engines it drives and to open a port in the
// firewall — two things, both spelled out in a sudoers file the installer
// writes, and nothing else.
//
// When mor is root — the command line, the installer — the commands run
// directly. There is no sudo to ask, and on a minimal server there may be no
// sudo at all.
package priv

import (
	"os"
	"os/exec"
)

// root is a variable so tests can drive both paths without being root.
var root = os.Geteuid() == 0

// Command builds the command, adding sudo when mor is not root.
//
// -n means never prompt: there is no terminal to type a password into, and a
// prompt would hang the daemon instead of failing.
func Command(name string, args ...string) *exec.Cmd {
	if root {
		return exec.Command(name, args...)
	}
	return exec.Command("sudo", append([]string{"-n", name}, args...)...)
}

// Root reports whether mor is running with full privileges.
func Root() bool { return root }
