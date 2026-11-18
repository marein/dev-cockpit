package browser

import (
	"bytes"
	"testing"
)

func TestTheScriptsDefineTheirNames(t *testing.T) {
	for _, one := range []struct {
		script []byte
		head   string
		define string
	}{
		{PickScript(), "// dcPicker", "\nfunction dcPicker(options) {"},
		{ConsoleScript(), "// dcConsole", "\nglobalThis.dcConsole = function (options) {"},
		{DrawScript(), "// dcDrawing", "\nfunction dcDrawing(options) {"},
	} {
		if !bytes.HasPrefix(one.script, []byte(one.head)) || !bytes.Contains(one.script, []byte(one.define)) {
			t.Fatalf("the script %s does not define its name", one.head)
		}
	}
}
