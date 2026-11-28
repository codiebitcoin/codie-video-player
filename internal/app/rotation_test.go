package app

import "testing"

func TestRotateQuarter(t *testing.T) {
	a := &app{}
	for i, want := range []int{90, 180, 270, 0} {
		a.rotateQuarter()
		if a.rotation != want {
			t.Errorf("turn %d: rotation = %d, want %d", i+1, a.rotation, want)
		}
	}
	if a.rotation != 0 {
		t.Errorf("after a full circle rotation = %d, want 0", a.rotation)
	}
}
