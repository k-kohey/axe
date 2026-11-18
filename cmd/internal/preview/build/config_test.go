package build

import (
	"path/filepath"
	"testing"
)

func TestProjectConfigPaths(t *testing.T) {
	tests := []struct {
		name        string
		pc          ProjectConfig
		wantPrimary string
		wantRoot    string
	}{
		{
			name:        "project",
			pc:          ProjectConfig{Project: "/src/App/App.xcodeproj", Root: "/ignored"},
			wantPrimary: "/src/App/App.xcodeproj",
			wantRoot:    "/src/App",
		},
		{
			name:        "workspace wins over project",
			pc:          ProjectConfig{Workspace: "/src/App.xcworkspace", Project: "/src/App/App.xcodeproj"},
			wantPrimary: "/src/App.xcworkspace",
			wantRoot:    "/src",
		},
		{
			name:        "build info without project",
			pc:          ProjectConfig{BuildInfoPath: "/tmp/bi.json", Root: "/src/App"},
			wantPrimary: "/tmp/bi.json",
			wantRoot:    "/src/App",
		},
		{
			name:        "build info with detected project",
			pc:          ProjectConfig{BuildInfoPath: "/tmp/bi.json", Project: "/src/App/App.xcodeproj", Root: "/ignored"},
			wantPrimary: "/tmp/bi.json",
			wantRoot:    "/src/App",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pc.PrimaryPath(); got != filepath.FromSlash(tt.wantPrimary) {
				t.Errorf("PrimaryPath() = %q, want %q", got, tt.wantPrimary)
			}
			if got := tt.pc.ProjectRoot(); got != filepath.FromSlash(tt.wantRoot) {
				t.Errorf("ProjectRoot() = %q, want %q", got, tt.wantRoot)
			}
		})
	}
}
