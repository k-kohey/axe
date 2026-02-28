package preview

import (
	"sort"
	"sync"
	"testing"

	"github.com/k-kohey/axe/internal/preview/analysis"
)

func TestCaptureQueue_BasicFlow(t *testing.T) {
	q := newCaptureQueue()
	q.Enqueue(&captureJob{fileIdx: 0, fb: fileBlocks{file: "a.swift"}})
	q.Enqueue(&captureJob{fileIdx: 1, fb: fileBlocks{file: "b.swift"}})
	q.Seal()

	job1 := q.Dequeue()
	if job1 == nil || job1.fileIdx != 0 {
		t.Fatalf("expected job 0, got %v", job1)
	}
	q.Finish()

	job2 := q.Dequeue()
	if job2 == nil || job2.fileIdx != 1 {
		t.Fatalf("expected job 1, got %v", job2)
	}
	q.Finish()

	// Queue exhausted.
	job3 := q.Dequeue()
	if job3 != nil {
		t.Fatalf("expected nil after all jobs done, got fileIdx=%d", job3.fileIdx)
	}
}

func TestCaptureQueue_Retry(t *testing.T) {
	q := newCaptureQueue()
	q.Enqueue(&captureJob{fileIdx: 0, fb: fileBlocks{file: "a.swift"}})
	q.Seal()

	job := q.Dequeue()
	if job == nil {
		t.Fatal("expected a job")
	}
	job.retryCount++
	q.Retry(job)

	// Retried job should be dequeued again.
	retried := q.Dequeue()
	if retried == nil {
		t.Fatal("expected retried job")
	}
	if retried.retryCount != 1 {
		t.Fatalf("expected retryCount=1, got %d", retried.retryCount)
	}
	q.Finish()

	// Queue exhausted.
	if q.Dequeue() != nil {
		t.Fatal("expected nil after retry completed")
	}
}

func TestCaptureQueue_RetryBeforeFinish(t *testing.T) {
	// Verify Retry's atomicity: other workers must not see nil while a retry is pending.
	q := newCaptureQueue()
	q.Enqueue(&captureJob{fileIdx: 0, fb: fileBlocks{file: "a.swift"}})
	q.Seal()

	// Worker 1 dequeues.
	job := q.Dequeue()
	if job == nil {
		t.Fatal("expected job")
	}

	// Worker 2 is blocked in Dequeue (inflight=1, so it waits).
	// Simulate: if Retry were not atomic (Enqueue + Finish separately),
	// there would be a brief state with inflight==0 and len(jobs)==0.
	// Retry ensures this doesn't happen.
	job.retryCount++
	q.Retry(job)

	// Worker 2 should get the retried job.
	retried := q.Dequeue()
	if retried == nil {
		t.Fatal("expected retried job, got nil (race condition in Retry)")
	}
	q.Finish()

	if q.Dequeue() != nil {
		t.Fatal("expected nil after all done")
	}
}

func TestCaptureQueue_ConcurrentWorkers(t *testing.T) {
	const numJobs = 20
	const numWorkers = 4

	q := newCaptureQueue()
	for i := range numJobs {
		q.Enqueue(&captureJob{
			fileIdx: i,
			fb:      fileBlocks{file: "file.swift", previews: []analysis.PreviewBlock{{Title: "p"}}},
		})
	}
	q.Seal()

	var mu sync.Mutex
	var seen []int

	var wg sync.WaitGroup
	for range numWorkers {
		wg.Go(func() {
			for {
				job := q.Dequeue()
				if job == nil {
					return
				}
				mu.Lock()
				seen = append(seen, job.fileIdx)
				mu.Unlock()
				q.Finish()
			}
		})
	}
	wg.Wait()

	if len(seen) != numJobs {
		t.Fatalf("expected %d jobs processed, got %d", numJobs, len(seen))
	}

	// Verify no duplicates.
	sort.Ints(seen)
	for i := range numJobs {
		if seen[i] != i {
			t.Fatalf("expected job %d at position %d, got %d", i, i, seen[i])
		}
	}
}

func TestCaptureQueue_AllRetried(t *testing.T) {
	const numJobs = 3
	q := newCaptureQueue()
	for i := range numJobs {
		q.Enqueue(&captureJob{fileIdx: i, fb: fileBlocks{file: "f.swift"}})
	}
	q.Seal()

	// Each job retried once, then finished.
	for range numJobs {
		job := q.Dequeue()
		if job == nil {
			t.Fatal("unexpected nil")
		}
		if job.retryCount == 0 {
			job.retryCount++
			q.Retry(job)
		} else {
			q.Finish()
		}
	}

	// Drain retried jobs.
	for range numJobs {
		job := q.Dequeue()
		if job == nil {
			// Some retries may have been processed in the first loop.
			break
		}
		q.Finish()
	}

	if q.Dequeue() != nil {
		t.Fatal("expected nil after all retries completed")
	}
}
