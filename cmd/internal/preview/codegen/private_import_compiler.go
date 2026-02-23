package codegen

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/k-kohey/axe/internal/preview/analysis"
)

// PrivateImportCompiler implements ThunkCompiler using the @_private(sourceFile:)
// import approach. It parses tracked files, generates a combined thunk with
// dynamic replacement extensions, and compiles it into a dylib.
type PrivateImportCompiler struct {
	moduleName string
	thunkDir   string
	buildDir   string
	cfg        CompileConfig
	tc         ToolchainRunner
}

// NewPrivateImportCompiler creates a PrivateImportCompiler.
func NewPrivateImportCompiler(moduleName, thunkDir, buildDir string, cfg CompileConfig, tc ToolchainRunner) *PrivateImportCompiler {
	return &PrivateImportCompiler{
		moduleName: moduleName,
		thunkDir:   thunkDir,
		buildDir:   buildDir,
		cfg:        cfg,
		tc:         tc,
	}
}

// Compile generates a thunk for the source file and compiles it into a signed dylib.
//
// The pipeline:
//  1. Parse all tracked files (lenient — parse errors are skipped).
//  2. If no files have types, fall back to parsing the source file only.
//  3. Generate a combined thunk with @_private imports and @_dynamicReplacement.
//  4. Compile the thunk into a dylib and codesign it.
func (c *PrivateImportCompiler) Compile(ctx context.Context, opts CompileOpts) (string, error) {
	files := ParseTrackedFiles(opts.SourceFile, opts.TrackedFiles)

	// Fallback: if no tracked files have types, try source file only.
	// This handles the case where tracked dependency files have no parseable
	// types (e.g. during editing) but the source file itself is valid.
	if len(files) == 0 {
		types, imports, err := analysis.SourceFile(opts.SourceFile)
		if err != nil {
			return "", fmt.Errorf("no types found in tracked files or source file: %w", err)
		}
		if len(types) == 0 {
			return "", fmt.Errorf("no types found in tracked files")
		}
		files = append(files, analysis.FileThunkData{
			FileName: filepath.Base(opts.SourceFile),
			AbsPath:  opts.SourceFile,
			Types:    types,
			Imports:  imports,
		})
	}

	thunkPath, err := GenerateCombinedThunk(files, c.moduleName, c.thunkDir, opts.PreviewSelector, opts.SourceFile)
	if err != nil {
		return "", fmt.Errorf("thunk: %w", err)
	}

	dylibPath, err := CompileThunk(ctx, thunkPath, c.cfg, c.thunkDir, c.buildDir, opts.ReloadCounter, opts.SourceFile, c.tc)
	if err != nil {
		return "", fmt.Errorf("compile: %w", err)
	}

	return dylibPath, nil
}
