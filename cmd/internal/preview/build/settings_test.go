package build

import "testing"

func TestSettingsClone_DeepCopySlices(t *testing.T) {
	t.Parallel()

	orig := &Settings{
		ModuleName:          "TestModule",
		ExtraIncludePaths:   []string{"/I/orig"},
		ExtraFrameworkPaths: []string{"/F/orig"},
		ExtraModuleMapFiles: []string{"/M/orig.modulemap"},
	}

	clone := orig.Clone()
	if clone == orig {
		t.Fatal("Clone returned the same pointer")
	}

	clone.ExtraIncludePaths[0] = "/I/clone"
	clone.ExtraFrameworkPaths[0] = "/F/clone"
	clone.ExtraModuleMapFiles[0] = "/M/clone.modulemap"

	clone.ExtraIncludePaths = append(clone.ExtraIncludePaths, "/I/clone2")
	clone.ExtraFrameworkPaths = append(clone.ExtraFrameworkPaths, "/F/clone2")
	clone.ExtraModuleMapFiles = append(clone.ExtraModuleMapFiles, "/M/clone2.modulemap")

	if got := orig.ExtraIncludePaths[0]; got != "/I/orig" {
		t.Fatalf("orig include path mutated: got %q", got)
	}
	if got := orig.ExtraFrameworkPaths[0]; got != "/F/orig" {
		t.Fatalf("orig framework path mutated: got %q", got)
	}
	if got := orig.ExtraModuleMapFiles[0]; got != "/M/orig.modulemap" {
		t.Fatalf("orig modulemap path mutated: got %q", got)
	}
	if len(orig.ExtraIncludePaths) != 1 || len(orig.ExtraFrameworkPaths) != 1 || len(orig.ExtraModuleMapFiles) != 1 {
		t.Fatalf("orig slice lengths mutated: include=%d framework=%d modulemap=%d",
			len(orig.ExtraIncludePaths), len(orig.ExtraFrameworkPaths), len(orig.ExtraModuleMapFiles))
	}
}
