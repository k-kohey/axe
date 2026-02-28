package report

import "sync"

// captureJob is a single capture work item (one file with all its previews).
type captureJob struct {
	fileIdx    int        // index into the original blocks slice (for result ordering)
	fb         fileBlocks // file + all preview blocks
	retryCount int        // number of queue re-submissions (capped at captureMaxRetries)
}

// captureQueue is a work queue for preview capture jobs.
// Workers call Dequeue to obtain jobs and Finish or Retry to report completion.
// All methods are safe for concurrent use.
type captureQueue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	jobs     []*captureJob
	inflight int
	sealed   bool // true after Seal(): no more initial job submissions
}

func newCaptureQueue() *captureQueue {
	q := &captureQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Enqueue adds a job to the queue.
func (q *captureQueue) Enqueue(job *captureJob) {
	q.mu.Lock()
	q.jobs = append(q.jobs, job)
	q.mu.Unlock()
	q.cond.Signal()
}

// Seal indicates that all initial jobs have been enqueued.
// Retry-based submissions are still accepted after Seal.
func (q *captureQueue) Seal() {
	q.mu.Lock()
	q.sealed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}

// Dequeue returns the next job, blocking if the queue is empty but in-flight
// jobs exist (they may produce retries). Returns nil when all work is done.
func (q *captureQueue) Dequeue() *captureJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		if len(q.jobs) > 0 {
			job := q.jobs[0]
			q.jobs = q.jobs[1:]
			q.inflight++
			return job
		}
		// Queue empty: terminate if sealed and nothing in-flight.
		if q.sealed && q.inflight == 0 {
			return nil
		}
		q.cond.Wait()
	}
}

// Retry re-enqueues a job and decrements the in-flight count atomically.
// This ordering prevents a transient state where inflight==0 && len(jobs)==0,
// which would cause other workers to incorrectly conclude that all work is done.
// The caller must increment job.retryCount before calling Retry.
func (q *captureQueue) Retry(job *captureJob) {
	q.mu.Lock()
	q.jobs = append(q.jobs, job)
	q.inflight--
	q.mu.Unlock()
	q.cond.Signal()
}

// Finish signals that a job completed (success or final failure).
func (q *captureQueue) Finish() {
	q.mu.Lock()
	q.inflight--
	done := q.sealed && q.inflight == 0 && len(q.jobs) == 0
	q.mu.Unlock()
	if done {
		q.cond.Broadcast()
	}
}
