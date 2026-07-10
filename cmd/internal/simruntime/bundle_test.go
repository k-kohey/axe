package simruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundleIDFromApp(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "Example.app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.example.App</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := bundleIDFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	if got != "com.example.App" {
		t.Fatalf("bundle id = %q", got)
	}
}
