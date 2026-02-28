package build

import "context"

// Runner abstracts xcodebuild operations for testability.
type Runner interface {
	FetchBuildSettings(ctx context.Context, args []string) ([]byte, error)
	Build(ctx context.Context, args []string) ([]byte, error)
}
