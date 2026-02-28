package preview

import (
	"testing"

	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/preview/protocol"
)

// TestStreamManager_UsesSharedPreparer verifies that the StreamManager
// stores and exposes the Preparer passed at construction.
func TestStreamManager_UsesSharedPreparer(t *testing.T) {
	t.Parallel()

	output := []byte(`    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`)
	br := &fakeBuildRunner{fetchOutput: output}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	preparer := build.NewPreparer(pc, build.ProjectDirs{Build: t.TempDir()}, false, br)

	sm := NewStreamManager(newFakeDevicePool(), protocol.NewEventWriter(&syncBuffer{}),
		pc, "", preparer,
		br, &fakeToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, &fakeFileCopier{}, &errSourceLister{}, false)

	if sm.preparer != preparer {
		t.Error("StreamManager.preparer should be the Preparer passed at construction")
	}
}
