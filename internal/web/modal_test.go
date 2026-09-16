package web

import (
	"strings"
	"testing"
)

// Modal behaviour is shared by every server-rendered page, so keep its
// accessibility contract in the normal Go test gate even when browser
// dependencies are unavailable in a minimal checkout.
func TestModalAccessibilityStyleContract(t *testing.T) {
	styles, err := files.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("read modal styles: %v", err)
	}
	css := string(styles)
	for _, required := range []string{
		"dialog{width:min(42rem,calc(100vw - 2rem));max-width:none;max-height:calc(100dvh - 2rem)",
		"dialog::backdrop{background:rgba(5,15,20,.72)",
		"dialog article>.dialog-body{display:flex;flex:1 1 auto;min-height:0",
		"overflow-y:auto;overscroll-behavior:contain",
		"dialog article>.dialog-body>form{display:flex;flex:0 0 auto;min-height:0",
		"dialog article>.dialog-body>form.dialog-secondary-form{display:block",
		"dialog article>.dialog-body>form>footer{position:sticky",
		"dialog article>.dialog-body>form+form{position:sticky",
	} {
		if !strings.Contains(css, required) {
			t.Fatalf("modal style contract is missing %q", required)
		}
	}
}

func TestModalAccessibilityScriptContract(t *testing.T) {
	script, err := files.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read modal script: %v", err)
	}
	js := string(script)
	for _, required := range []string{
		"const enhanceLegacyDialog=dialog=>",
		"className='dialog-body'",
		"forms=[...wrapper.querySelectorAll(':scope > form')]",
		"dialog-secondary-form",
		"dialogFocusTarget=dialog=>dialog.querySelector('[autofocus]')",
		"nativeShowModal.call(dialog)",
		"modalReturnFocus.delete(dialog)",
		"event.preventDefault()",
	} {
		if !strings.Contains(js, required) {
			t.Fatalf("modal script contract is missing %q", required)
		}
	}
	if strings.Contains(js, "querySelector('[autofocus],button,input,select,textarea") {
		t.Fatal("focus initialization must not use an order-dependent selector")
	}
}
