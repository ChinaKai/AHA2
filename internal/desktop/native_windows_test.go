//go:build windows

package desktop

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestNativeHelperRejectsMalformedTarget(t *testing.T) {
	if os.Getenv("AHA_NATIVE_HELPER_TEST") != "1" {
		t.Skip("opt-in helper smoke test; does not enumerate or access a window")
	}
	p := NativeProvider()
	t.Cleanup(func() { _ = p.(*nativeProvider).Close() })
	if supported, reason := p.Support(); !supported {
		t.Fatalf("helper unavailable: %s", reason)
	}
	_, err := p.Observe(context.Background(), Window{ID: "invalid-fixture-id"})
	if err == nil || !strings.Contains(err.Error(), "stale_target") {
		t.Fatalf("helper did not reject malformed target: %v", err)
	}
}

func TestNativeEncodedCommand(t *testing.T) {
	script := "Write-Output '\u4e2d\u6587'"
	data, err := base64.StdEncoding.DecodeString(encodeNativeCommand(script))
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	if string(utf16.Decode(units)) != script {
		t.Fatal("script encoding changed content")
	}
	if len(encodeNativeCommand(nativeBootstrap)) > 24000 {
		t.Fatal("bootstrap approaches Windows command-line size limit")
	}
}
