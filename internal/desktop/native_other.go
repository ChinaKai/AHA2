//go:build !windows

package desktop

func NativeProvider() Provider {
	return &nativeProvider{reason: "desktop_windows_required"}
}
