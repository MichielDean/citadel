package castellarius

import "sync"

// WorkerStatus indicates whether an operator is available.
type WorkerStatus string

const (
	WorkerAvailable WorkerStatus = "available"
	WorkerBusy WorkerStatus = "busy"
)

// Worker is a named slot in a per-repo worker pool.
type Worker struct {
	Name      string
	RepoName  string
	Status    WorkerStatus
	DropletID string // non-empty when busy
	Step      string // current step when busy
}

// WorkerPool manages named workers for a single repository.
// Workers don't cross repo boundaries.
type WorkerPool struct {
	mu      sync.Mutex
	repo    string
	workers []*Worker
}

// NewWorkerPool creates a pool with the given named workers.
func NewWorkerPool(repo string, names []string) *WorkerPool {
	workers := make([]*Worker, len(names))
	for i, name := range names {
		workers[i] = &Worker{
			Name:     name,
			RepoName: repo,
			Status:   WorkerAvailable,
		}
	}
	return &WorkerPool{repo: repo, workers: workers}
}

// AvailableWorker returns the first available operator, or nil if all are busy.
func (p *WorkerPool) AvailableWorker() *Worker {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.workers {
		if w.Status == WorkerAvailable {
			return w
		}
	}
	return nil
}

// BusyCount returns the number of workers currently assigned work.
func (p *WorkerPool) BusyCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, w := range p.workers {
		if w.Status == WorkerBusy {
			n++
		}
	}
	return n
}

// Assign marks a worker as busy with the given droplet and step.
func (p *WorkerPool) Assign(w *Worker, dropletID, step string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Status = WorkerBusy
	w.DropletID = dropletID
	w.Step = step
}

// Release marks an operator as available and clears its assignment.
func (p *WorkerPool) Release(w *Worker) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Status = WorkerAvailable
	w.DropletID = ""
	w.Step = ""
}

// IsWorkerBusy returns true if the named worker exists and is currently busy.
func (p *WorkerPool) IsWorkerBusy(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.workers {
		if w.Name == name {
			return w.Status == WorkerBusy
		}
	}
	return false
}

// FindAndClaimWorkerByName atomically finds the named worker and marks it busy
// if it is currently available. Returns nil if the worker is not found or is
// already busy — this prevents races between the heartbeat and the main tick.
func (p *WorkerPool) FindAndClaimWorkerByName(name string) *Worker {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.workers {
		if w.Name == name && w.Status == WorkerAvailable {
			w.Status = WorkerBusy
			return w
		}
	}
	return nil
}

// FindWorkerByName returns the named worker without changing its state, or nil
// if no worker with that name exists. Safe for use with pool.Release.
func (p *WorkerPool) FindWorkerByName(name string) *Worker {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.workers {
		if w.Name == name {
			return w
		}
	}
	return nil
}
