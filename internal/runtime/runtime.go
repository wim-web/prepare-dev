// Package runtime is the prepare-dev runtime: questions and answers with the staged
// apply and recovery (SPEC-0001), verification runs, trace, status and review bundles.
// One Runtime serves one project directory; its methods are serialized by a mutex.
package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/store"
)

// Options configure Open.
type Options struct {
	Plugins       []Plugin
	RecoveryHooks *RecoveryHooks
}

// Runtime bundles the store and the services built on it.
type Runtime struct {
	mu        sync.Mutex
	Store     *store.Store
	Questions *Questions
	Runs      *Runs
	Trace     *Trace
	Status    *Status
	Recovery  RecoveryReport
}

// Open loads the journal, rebuilds the index, scans records for external changes and
// runs recovery before anything else is served.
func Open(root string, opts Options) (*Runtime, error) {
	st, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	rt := &Runtime{Store: st, Questions: &Questions{store: st}}
	// with a conflicting journal nothing is written, recovery included; neither when a
	// transaction cannot be recovered (its question file is gone). The daemon still
	// starts so that status can show the problems.
	if len(st.Blocked) == 0 && len(st.Unrecoverable) == 0 {
		rep, err := rt.Questions.Recover(opts.RecoveryHooks)
		if err != nil {
			return nil, err
		}
		rt.Recovery = rep
	}
	rt.Runs = newRuns(st, opts.Plugins, &rt.mu)
	rt.Trace = &Trace{store: st, questions: rt.Questions, runs: rt.Runs}
	rt.Status = &Status{store: st, questions: rt.Questions, runs: rt.Runs, trace: rt.Trace}
	return rt, nil
}

// Root is the project directory.
func (rt *Runtime) Root() string { return rt.Store.Root }

// Lock serializes access to the store; every operation runs under it.
func (rt *Runtime) Lock()   { rt.mu.Lock() }
func (rt *Runtime) Unlock() { rt.mu.Unlock() }

// Do runs fn under the lock.
func (rt *Runtime) Do(fn func() error) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return fn()
}

// WaitResult is returned by WaitQuestion.
type WaitResult struct {
	Status   string         `json:"status"` // answered | pending
	Question *core.Question `json:"question"`
	Answers  []core.Answer  `json:"answers,omitempty"`
}

// WaitQuestion returns at once when the question has unprocessed answers; otherwise
// it waits for a new answer, the timeout or ctx, without holding the lock.
func (rt *Runtime) WaitQuestion(ctx context.Context, questionID string, timeout time.Duration) (WaitResult, error) {
	rt.mu.Lock()
	if err := rt.Store.AssertAlive(); err != nil {
		rt.mu.Unlock()
		return WaitResult{}, err
	}
	res, err := rt.waitResult(questionID)
	if err != nil || res.Status == "answered" {
		rt.mu.Unlock()
		return res, err
	}
	ch := make(chan struct{}, 1)
	off := rt.Store.Emitter.On(func(name string, payload any) {
		if ev, ok := payload.(core.Event); ok && name == "event" && ev.Type == "answer.save" && ev.QuestionID == questionID {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	})
	rt.mu.Unlock()
	timer := time.NewTimer(timeout)
	select {
	case <-ch:
	case <-timer.C:
	case <-ctx.Done():
	}
	timer.Stop()
	off()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.waitResult(questionID)
}

func (rt *Runtime) waitResult(questionID string) (WaitResult, error) {
	q, err := rt.Questions.Read(questionID)
	if err != nil {
		return WaitResult{}, err
	}
	answers, err := rt.Questions.PendingFor(questionID)
	if err != nil {
		return WaitResult{}, err
	}
	if len(answers) > 0 {
		return WaitResult{Status: "answered", Question: q, Answers: answers}, nil
	}
	return WaitResult{Status: "pending", Question: q}, nil
}
