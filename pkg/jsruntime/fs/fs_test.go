package fs

import (
	"testing"

	"github.com/dop251/goja"
)

func TestFSModeUsesFallbackForStringEncoding(t *testing.T) {
	vm := goja.New()

	if got := fsMode(vm.ToValue("utf8"), 0o666); got != 0o666 {
		t.Fatalf("fsMode(encoding string) = %o, want fallback %o", got, 0o666)
	}
}
