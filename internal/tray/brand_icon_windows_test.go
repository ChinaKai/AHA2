//go:build windows

package tray

import "testing"

func TestCreateBrandIcon(t *testing.T) {
	icon := createBrandIcon()
	if icon == 0 {
		t.Fatal("CreateIconFromResourceEx rejected the AHA2 tray icon")
	}
	destroyBrandIcon(icon)
}
