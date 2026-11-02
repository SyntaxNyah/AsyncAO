package media

import "sync"

// Pool is a fixed set of workers, like a QThreadPool; the Manager starts a job only when one is free, so nothing queues here.
type Pool struct {
	name    string
	workers int
	// keep is how many workers only Now work may use.
	keep int
	busy int // Manager goroutine only
	jobs chan func()
	wg   sync.WaitGroup
}

// NewPool starts workers goroutines, keeping keep of them for Now work.
func NewPool(name string, workers, keep int) *Pool {
	p := &Pool{name: name, workers: workers, keep: keep, jobs: make(chan func(), workers)}
	p.wg.Add(workers)
	for range workers {
		go func() {
			defer p.wg.Done()
			for job := range p.jobs {
				job()
			}
		}()
	}
	return p
}

func (p *Pool) canStart(u Urgency) bool {
	if u == Now {
		return p.busy < p.workers
	}
	return p.busy < p.workers-p.keep
}

// Close stops the workers after their current job; a worker stuck in a job holds Close until the job returns.
func (p *Pool) Close() {
	close(p.jobs)
	p.wg.Wait()
}
