// Package systemd wraps the one systemctl dance every engine mor manages
// needs: clear the start-rate block before restarting, because a unit
// restarted too often in a short window refuses to start again until
// somebody says reset-failed — which is not something the owner of a VPN
// should have to know.
package systemd

import (
	"fmt"

	"mor/internal/priv"
)

// Restart brings a service up, clearing the start-rate block first.
func Restart(service string) error {
	_ = priv.Command("systemctl", "reset-failed", service).Run()
	out, err := priv.Command("systemctl", "restart", service).CombinedOutput()
	if err != nil {
		return fmt.Errorf("restart %s: %w: %s", service, err, out)
	}
	return nil
}
