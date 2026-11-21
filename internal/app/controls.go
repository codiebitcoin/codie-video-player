package app

import (
	"time"
)

// The controls over the stage know when to leave: while an episode plays
// and the pointer sits still, they fade away after a moment, and any move
// brings them back. Paused, broken, or away on the device, they stay.

// controlsIdle is how long the pointer sits still before the controls go.
const controlsIdle = 2600 * time.Millisecond

// wakeControls brings the controls out, and starts their leaving again.
func (a *app) wakeControls() {
	a.controlsOn = true
	a.controlsAt = time.Now()
	if a.controlsTmr != nil {
		a.controlsTmr.Stop()
		a.controlsTmr = nil
	}
	if a.controlsMayLeave() {
		a.controlsTmr = time.AfterFunc(controlsIdle, func() {
			a.post(func() {
				a.controlsTmr = nil
				if a.controlsOn && a.controlsMayLeave() && time.Since(a.controlsAt) >= controlsIdle-100*time.Millisecond {
					a.controlsOn = false
					if a.win != nil {
						a.win.Invalidate()
					}
				}
			})
		})
	}
}

// controlsMayLeave reports whether the controls may step out now: only
// while an episode plays here, whole and without a popover in the way.
func (a *app) controlsMayLeave() bool {
	if a.deviceOutput || a.pipActive() {
		return false
	}
	if a.speedOpen || a.drawerOpen {
		return false
	}
	p := a.player
	if p == nil || !p.Ready() || p.Err() != nil {
		return false
	}
	return p.Playing()
}
