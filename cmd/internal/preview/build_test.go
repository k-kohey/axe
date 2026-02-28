package preview

import "context"

// fakeBuildRunner is a test double for build.Runner, used by
// ensure_build_settings_test.go and stream_manager_test.go.
type fakeBuildRunner struct {
	fetchOutput []byte
	fetchErr    error
	buildOutput []byte
	buildErr    error

	// Captured args for assertions.
	fetchArgs []string
	buildArgs []string
}

func (f *fakeBuildRunner) FetchBuildSettings(_ context.Context, args []string) ([]byte, error) {
	f.fetchArgs = args
	return f.fetchOutput, f.fetchErr
}

func (f *fakeBuildRunner) Build(_ context.Context, args []string) ([]byte, error) {
	f.buildArgs = args
	return f.buildOutput, f.buildErr
}
