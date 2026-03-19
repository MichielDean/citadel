package castellarius

import "sync"

// AqueductStatus indicates whether a named aqueduct is idle or flowing.
type AqueductStatus string

const (
	AqueductIdle    AqueductStatus = "idle"
	AqueductFlowing AqueductStatus = "flowing"
)

// Aqueduct is a named pipeline instance that carries one droplet at a time
// through all cataractae in sequence. The Castellarius routes droplets into
// available aqueducts.
type Aqueduct struct {
	Name      string
	RepoName  string
	Status    AqueductStatus
	DropletID string // non-empty when flowing
	Step      string // current cataracta when flowing
}

// AqueductPool manages named aqueducts for a single repository.
// Aqueducts don't cross repo boundaries.
type AqueductPool struct {
	mu        sync.Mutex
	repo      string
	aqueducts []*Aqueduct
}

// NewAqueductPool creates a pool with the given named aqueducts.
func NewAqueductPool(repo string, names []string) *AqueductPool {
	aqueducts := make([]*Aqueduct, len(names))
	for i, name := range names {
		aqueducts[i] = &Aqueduct{
			Name:     name,
			RepoName: repo,
			Status:   AqueductIdle,
		}
	}
	return &AqueductPool{repo: repo, aqueducts: aqueducts}
}

// AvailableAqueduct returns the first idle aqueduct, or nil if all are flowing.
func (p *AqueductPool) AvailableAqueduct() *Aqueduct {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.aqueducts {
		if a.Status == AqueductIdle {
			return a
		}
	}
	return nil
}

// FlowingCount returns the number of aqueducts currently carrying a droplet.
func (p *AqueductPool) FlowingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, a := range p.aqueducts {
		if a.Status == AqueductFlowing {
			n++
		}
	}
	return n
}

// Assign marks an aqueduct as flowing with the given droplet and cataracta.
func (p *AqueductPool) Assign(a *Aqueduct, dropletID, step string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a.Status = AqueductFlowing
	a.DropletID = dropletID
	a.Step = step
}

// Release marks an aqueduct as idle and clears its assignment.
func (p *AqueductPool) Release(a *Aqueduct) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a.Status = AqueductIdle
	a.DropletID = ""
	a.Step = ""
}

// IsFlowing returns true if the named aqueduct exists and is currently flowing.
func (p *AqueductPool) IsFlowing(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.aqueducts {
		if a.Name == name {
			return a.Status == AqueductFlowing
		}
	}
	return false
}

// FindAndClaimByName atomically finds the named aqueduct and marks it flowing
// if currently idle. Returns nil if not found or already flowing.
func (p *AqueductPool) FindAndClaimByName(name string) *Aqueduct {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.aqueducts {
		if a.Name == name && a.Status == AqueductIdle {
			a.Status = AqueductFlowing
			return a
		}
	}
	return nil
}

// FindByName returns the named aqueduct without changing its state, or nil.
func (p *AqueductPool) FindByName(name string) *Aqueduct {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.aqueducts {
		if a.Name == name {
			return a
		}
	}
	return nil
}
