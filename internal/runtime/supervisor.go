package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ErrSupervisorClosed is returned by RunTurn / Register when the
// Supervisor (or the tenant's own goroutine) has already been shut down.
// Callers treat this as "service unavailable" rather than an internal
// fault.
var ErrSupervisorClosed = errors.New("runtime: supervisor closed")

// Supervisor orchestrates multiple per-tenant Runtimes in a single
// process. Each registered tenant owns its own goroutine that drains a
// jobs channel serially — intra-tenant turns never overlap, inter-tenant
// turns run concurrently.
//
// Supervisor does not construct tenants' wiring (provider / tools /
// session / hooks); callers pass a fully-built Runtime so Supervisor
// stays agnostic to CLI vs. server vs. test drivers. After Register,
// Supervisor takes ownership of the *Runtime and its Session — callers
// MUST NOT call rt.RunTurn or rt.Session().Close() directly while the
// tenant is registered; use Supervisor.RunTurn / Unregister / Close
// instead. Unregister and Close are the only places that close the
// Session.
//
// Thread-safety: every exported method is safe to call from any
// goroutine. The shutdown protocol is "close quit, never close jobs":
// senders select on both jobs <- job and <-agent.quit, so close signals
// propagate without racing concurrent sends.
type Supervisor struct {
	mu     sync.RWMutex
	agents map[string]*supervisedAgent
	wg     sync.WaitGroup
	closed atomic.Bool
}

// supervisedAgent binds one Runtime (one tenant) to one goroutine.
// Shutdown protocol:
//   - Unregister closes `quit` to signal the agent to stop.
//   - The agent exits its select loop, then in defer drains any queued
//     jobs (reply with ErrSupervisorClosed) and closes the Session.
//   - `done` is closed last by the agent so Unregister can wait on it.
//   - `jobs` is NEVER closed — that would race with concurrent sends.
type supervisedAgent struct {
	tenant TenantContext
	rt     *Runtime
	jobs   chan supervisorJob
	quit   chan struct{}
	done   chan struct{}
}

// supervisorJob is one RunTurn request travelling from Supervisor.RunTurn
// to the agent goroutine. result is single-use and buffered(1) so the
// agent goroutine never blocks on send even if the caller gave up.
type supervisorJob struct {
	ctx    context.Context
	input  string
	result chan supervisorResult
}

type supervisorResult struct {
	summary *TurnSummary
	err     error
}

// RegisterOpts carries per-agent knobs. Zero value is sensible:
// QueueSize == 0 is treated as 1 (pure back-pressure — caller blocks
// until the agent is ready to take the turn).
type RegisterOpts struct {
	// QueueSize is the jobs channel capacity. 0 → 1 (strict back-pressure).
	// A larger queue lets bursty callers fire and forget; Supervisor does
	// not drop jobs, only the receiving goroutine does — be conservative.
	QueueSize int
}

// NewSupervisor returns a ready-to-use Supervisor with no tenants
// registered.
func NewSupervisor() *Supervisor {
	return &Supervisor{agents: make(map[string]*supervisedAgent)}
}

// Register attaches a tenant's Runtime to the Supervisor. tenant.ID must
// be non-empty — Supervisor uses it as the routing key for RunTurn.
// Re-registering the same ID returns an error (callers must Unregister
// first). After this returns, Supervisor owns rt and will Close its
// Session on Unregister / Close.
func (s *Supervisor) Register(tenant TenantContext, rt *Runtime, opts RegisterOpts) error {
	if s.closed.Load() {
		return ErrSupervisorClosed
	}
	if tenant.ID == "" {
		return errors.New("runtime: supervisor requires non-empty tenant.ID")
	}
	if rt == nil {
		return errors.New("runtime: supervisor requires non-nil Runtime")
	}
	queue := opts.QueueSize
	if queue < 1 {
		queue = 1
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed.Load() {
		return ErrSupervisorClosed
	}
	if _, exists := s.agents[tenant.ID]; exists {
		return fmt.Errorf("runtime: tenant %q already registered", tenant.ID)
	}

	agent := &supervisedAgent{
		tenant: tenant,
		rt:     rt,
		jobs:   make(chan supervisorJob, queue),
		quit:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	s.agents[tenant.ID] = agent
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		agent.run()
	}()
	return nil
}

// Unregister stops the tenant's goroutine and closes the Session. Jobs
// queued but not yet picked up are drained by the agent and replied to
// with ErrSupervisorClosed, so callers never hang forever.
//
// ctx is honored only for the post-signal wait; the agent goroutine
// itself exits as soon as it observes `quit`.
func (s *Supervisor) Unregister(ctx context.Context, tenantID string) error {
	s.mu.Lock()
	agent, ok := s.agents[tenantID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("runtime: tenant %q not registered", tenantID)
	}
	delete(s.agents, tenantID)
	s.mu.Unlock()

	// Signal-only shutdown. DO NOT close agent.jobs — senders may still
	// be selecting on it. Senders observe <-agent.quit and bail out.
	close(agent.quit)

	select {
	case <-agent.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RunTurn dispatches userInput to the registered tenant's goroutine and
// blocks until RunTurn finishes there (or ctx is cancelled, or the
// Supervisor / tenant is shut down). Intra-tenant calls serialize on
// the tenant's jobs channel; inter-tenant calls are fully parallel.
func (s *Supervisor) RunTurn(ctx context.Context, tenantID, input string) (*TurnSummary, error) {
	if s.closed.Load() {
		return nil, ErrSupervisorClosed
	}
	s.mu.RLock()
	agent, ok := s.agents[tenantID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("runtime: tenant %q not registered", tenantID)
	}

	resultCh := make(chan supervisorResult, 1)
	job := supervisorJob{ctx: ctx, input: input, result: resultCh}

	// Enqueue. Three-way select: send lands / caller cancels / tenant
	// shut down. The quit case is what made this race-free — we never
	// close the jobs channel.
	select {
	case agent.jobs <- job:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-agent.quit:
		return nil, ErrSupervisorClosed
	}

	// Wait for result. If ctx fires while the agent is still executing
	// the turn, Runtime.RunTurn itself honors ctx and will return
	// promptly; we still observe its result via resultCh.
	select {
	case res := <-resultCh:
		return res.summary, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close unregisters every tenant and waits for all agent goroutines to
// exit. Further Register / RunTurn calls return ErrSupervisorClosed.
// Safe to call multiple times; second and later calls are no-ops.
func (s *Supervisor) Close(ctx context.Context) error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	s.mu.Lock()
	ids := make([]string, 0, len(s.agents))
	for id := range s.agents {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	for _, id := range ids {
		if err := s.Unregister(ctx, id); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}

	allDone := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(allDone)
	}()
	select {
	case <-allDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Len reports how many tenants are currently registered. Diagnostic
// helper; not on the hot path.
func (s *Supervisor) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.agents)
}

// run is the agent goroutine body. It exits when `quit` is closed. On
// exit it drains any queued jobs (replying with ErrSupervisorClosed to
// unblock callers waiting on resultCh) and closes the Runtime's Session.
// Session.Close is guaranteed to run exactly once.
func (a *supervisedAgent) run() {
	defer close(a.done)
	defer func() {
		// Drain queued jobs — reply with closed error so senders don't
		// hang on their resultCh.
		for {
			select {
			case job := <-a.jobs:
				job.result <- supervisorResult{err: ErrSupervisorClosed}
			default:
				if sess := a.rt.Session(); sess != nil {
					_ = sess.Close()
				}
				return
			}
		}
	}()
	for {
		select {
		case <-a.quit:
			return
		case job := <-a.jobs:
			summary, err := a.rt.RunTurn(job.ctx, job.input)
			job.result <- supervisorResult{summary: summary, err: err}
		}
	}
}
