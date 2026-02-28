package report

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/k-kohey/axe/internal/platform"
	"github.com/k-kohey/axe/internal/preview/build"
	"golang.org/x/sync/errgroup"
)

const defaultMaxConcurrency = 4

// effectiveConcurrency returns the worker count for parallel capture.
func effectiveConcurrency(totalFiles, requested int) int {
	limit := defaultMaxConcurrency
	if requested > 0 {
		limit = requested
	}
	if totalFiles < limit {
		return totalFiles
	}
	return limit
}

// setupReportPool creates a DevicePool and resolves the default device spec.
func setupReportPool(ctx context.Context) (pool *platform.DevicePool, setPath, deviceType, runtime string, err error) {
	simctl := &platform.RealSimctlRunner{}
	deviceType, runtime, err = platform.FindDefaultDeviceSpec(simctl)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("resolving device spec: %w", err)
	}
	setPath, err = platform.AxeDeviceSetPath()
	if err != nil {
		return nil, "", "", "", err
	}
	if mkErr := os.MkdirAll(setPath, 0o755); mkErr != nil {
		return nil, "", "", "", fmt.Errorf("creating device set directory: %w", mkErr)
	}
	pool = platform.NewDevicePool(simctl, setPath)
	if cleanErr := pool.CleanupOrphans(ctx); cleanErr != nil {
		slog.Warn("orphan cleanup failed", "err", cleanErr)
	}
	return pool, setPath, deviceType, runtime, nil
}

// allFailures builds a captureResult where every preview in every file is failed.
func allFailures(blocks []fileBlocks, err error) captureResult {
	var result captureResult
	for _, fb := range blocks {
		for i, pb := range fb.previews {
			result.failures = append(result.failures, captureFailure{
				file:      fb.file,
				index:     i,
				title:     pb.Title,
				startLine: pb.StartLine,
				err:       err,
			})
		}
	}
	return result
}

// runParallelCapture orchestrates multi-simulator capture.
// It creates a DevicePool, pre-warms the build cache, acquires devices,
// and dispatches file-level jobs to worker goroutines.
func runParallelCapture(ctx context.Context, opts ReportOptions, blocks []fileBlocks,
	preparer *build.Preparer, failFast bool) captureResult {

	// 1. DevicePool setup
	pool, setPath, deviceType, runtime, err := setupReportPool(ctx)
	if err != nil {
		return allFailures(blocks, err)
	}
	defer pool.ShutdownAll(context.Background())
	defer pool.GarbageCollect(context.Background())

	// 2. Build prewarm + device acquisition in parallel
	conc := effectiveConcurrency(len(blocks), opts.Concurrency)
	slog.Info("parallel capture", "concurrency", conc, "files", len(blocks))

	devices := make([]string, conc)
	{
		g, gctx := errgroup.WithContext(ctx)
		// Prewarm build cache
		g.Go(func() error {
			_, err := preparer.Prepare(gctx)
			return err
		})
		// Acquire devices concurrently
		for i := range conc {
			g.Go(func() error {
				udid, err := pool.Acquire(gctx, deviceType, runtime)
				if err != nil {
					return err
				}
				devices[i] = udid
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return allFailures(blocks, err)
		}
	}

	// 3. Queue creation + job submission
	queue := newCaptureQueue()
	for fileIdx, fb := range blocks {
		queue.Enqueue(&captureJob{
			fileIdx: fileIdx,
			fb:      fb,
		})
	}
	queue.Seal()

	// 4. Result storage (preserves file x preview ordering)
	type outcome struct {
		capture *reportCapture
		failure *captureFailure
	}
	results := make([][]outcome, len(blocks))
	for i, fb := range blocks {
		results[i] = make([]outcome, len(fb.previews))
	}
	var resultMu sync.Mutex

	// 5. Worker goroutines
	var wg sync.WaitGroup
	var firstErr atomic.Value // for failFast

	for workerIdx := range conc {
		wg.Add(1)
		udid := devices[workerIdx]
		go func() {
			defer wg.Done()

			for {
				if failFast {
					if v := firstErr.Load(); v != nil {
						return
					}
				}

				job := queue.Dequeue()
				if job == nil {
					return // all work done
				}

				// Capture all previews in this file sequentially.
				var fileErr error
				for i, pb := range job.fb.previews {
					fmt.Fprintf(os.Stderr, "[worker %d] Capturing %s (preview %d)\n",
						workerIdx, filepath.Base(job.fb.file), i)
					png, err := captureOnce(opts, job.fb.file, i, preparer, udid, setPath)
					if err != nil {
						fileErr = err
						break // retry the whole file
					}
					resultMu.Lock()
					results[job.fileIdx][i] = outcome{
						capture: &reportCapture{
							file:      job.fb.file,
							index:     i,
							title:     pb.Title,
							startLine: pb.StartLine,
							png:       append([]byte(nil), png...),
						},
					}
					resultMu.Unlock()
				}

				if fileErr != nil {
					if job.retryCount < captureMaxRetries-1 {
						job.retryCount++
						slog.Warn("file capture failed, retrying",
							"file", filepath.Base(job.fb.file),
							"attempt", job.retryCount, "err", fileErr)
						// Clear partial results before retry.
						resultMu.Lock()
						for i := range results[job.fileIdx] {
							results[job.fileIdx][i] = outcome{}
						}
						resultMu.Unlock()
						time.Sleep(time.Duration(job.retryCount) * 500 * time.Millisecond)
						queue.Retry(job)
						continue
					}

					// Final failure: record all previews in this file as failed.
					resultMu.Lock()
					for i, pb := range job.fb.previews {
						results[job.fileIdx][i] = outcome{
							failure: &captureFailure{
								file:      job.fb.file,
								index:     i,
								title:     pb.Title,
								startLine: pb.StartLine,
								err:       fileErr,
							},
						}
					}
					resultMu.Unlock()
					queue.Finish()

					if failFast {
						firstErr.CompareAndSwap(nil, fileErr)
					}
					continue
				}

				// File succeeded.
				queue.Finish()
			}
		}()
	}
	wg.Wait()

	// 6. Flatten results in file-order, preview-order.
	//    In failFast mode, workers may have left some jobs unprocessed.
	//    Fill any zero-value outcomes as failures so callers see complete results.
	var failFastErr error
	if failFast {
		if v := firstErr.Load(); v != nil {
			failFastErr, _ = v.(error)
		}
	}

	var merged captureResult
	for fileIdx, fileResults := range results {
		for prevIdx, o := range fileResults {
			switch {
			case o.capture != nil:
				merged.captures = append(merged.captures, *o.capture)
			case o.failure != nil:
				merged.failures = append(merged.failures, *o.failure)
			default:
				// Unprocessed job (failFast aborted before reaching it).
				pb := blocks[fileIdx].previews[prevIdx]
				merged.failures = append(merged.failures, captureFailure{
					file:      blocks[fileIdx].file,
					index:     prevIdx,
					title:     pb.Title,
					startLine: pb.StartLine,
					err:       fmt.Errorf("skipped due to earlier failure: %w", failFastErr),
				})
			}
		}
	}
	return merged
}

// runReportPNGParallel is the parallel variant of runReportPNG.
func runReportPNGParallel(opts ReportOptions, blocks []fileBlocks, preparer *build.Preparer) error {
	outputIsDir, err := resolveOutputMode(opts.Output, blocks)
	if err != nil {
		return err
	}
	if outputIsDir {
		if err := os.MkdirAll(opts.Output, 0o755); err != nil {
			return fmt.Errorf("creating output directory: %w", err)
		}
		if err := checkOutputCollisions(opts.Output, blocks); err != nil {
			return err
		}
	} else {
		parentDir := filepath.Dir(opts.Output)
		if err := os.MkdirAll(parentDir, 0o755); err != nil {
			return fmt.Errorf("creating output parent directory: %w", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	result := runParallelCapture(ctx, opts, blocks, preparer, true)
	if len(result.failures) > 0 {
		return result.failures[0].err
	}

	for _, c := range result.captures {
		outputPath := computeOutputPath(opts.Output, c.file, c.index, outputIsDir)
		if err := os.WriteFile(outputPath, c.png, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// runReportDocumentParallel is the parallel variant of runReportDocument.
func runReportDocumentParallel(
	opts ReportOptions,
	blocks []fileBlocks,
	reportFileName string,
	render func([]reportCapture, []captureFailure, string, string) (string, error),
	preparer *build.Preparer,
) error {
	reportPath, assetsDir, err := prepareReportOutputPaths(opts.Output, reportFileName)
	if err != nil {
		return err
	}

	slog.Info("parallel report capture begin", "format", reportFileName, "fileCount", len(blocks))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	result := runParallelCapture(ctx, opts, blocks, preparer, false)

	slog.Info("parallel report capture done",
		"captureCount", len(result.captures), "failureCount", len(result.failures))

	var capturesWithRefs []reportCapture
	if len(result.captures) > 0 {
		capturesWithRefs, err = writeReportAssets(assetsDir, filepath.Dir(reportPath), result.captures)
		if err != nil {
			return err
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		slog.Warn("failed to get working directory", "err", err)
	}
	version := resolveVersion()
	content, err := render(capturesWithRefs, result.failures, cwd, version)
	if err != nil {
		return fmt.Errorf("rendering report: %w", err)
	}
	if err := os.WriteFile(reportPath, []byte(content), 0o644); err != nil {
		return err
	}
	slog.Info("parallel report written", "destination", reportPath, "bytes", len(content))

	if opener, lookErr := exec.LookPath("open"); lookErr == nil {
		if err := exec.Command(opener, reportPath).Start(); err != nil {
			slog.Warn("failed to open report", "err", err)
		}
	}

	if len(result.failures) > 0 {
		return fmt.Errorf("%d of %d preview captures failed (report generated with partial results)",
			len(result.failures), len(result.captures)+len(result.failures))
	}
	return nil
}
