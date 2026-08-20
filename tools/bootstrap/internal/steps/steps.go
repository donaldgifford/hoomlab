// Package steps is the bootstrap CLI's convergence engine
// (DESIGN-0001): a stage is a list of Steps, each with a Check that
// reads the world and an Apply that changes it. The runner skips done
// steps and applies pending ones in order — re-running a stage after
// any interruption converges because completed steps check as done.
// There are no plan files and no state; the config and the world are
// the only truths.
package steps

import (
	"context"
	"fmt"
	"io"
	"log/slog"
)

// Step is one convergent unit of work. Check reports whether the world
// already matches the desired state; Apply makes it so and returns
// only after the change is effective (PVE task waits live inside
// Apply). Check must never write.
type Step struct {
	Name  string
	Check func(ctx context.Context) (done bool, err error)
	Apply func(ctx context.Context) error
}

// Runner executes a stage's steps in order. The zero value runs with
// slog.Default and no dry-run; construct with the fields you need.
type Runner struct {
	// DryRun reports what would happen instead of applying: every
	// step's Check still runs (checks never write), pending steps are
	// listed on Out, and no Apply is called.
	DryRun bool
	// Out receives the dry-run report. Nil discards it.
	Out io.Writer
	// Log receives step progress. Nil means slog.Default().
	Log *slog.Logger
}

// Run converges the stage: for each step in order, Check, skip when
// done, otherwise Apply. The first failure stops the run — a re-run
// picks up where it left off because everything already applied
// checks as done. In dry-run mode it never applies; a Check error is
// reported as unknown state rather than stopping the survey. It
// returns the number of steps applied (pending, in dry-run).
func (r *Runner) Run(ctx context.Context, stage []Step) (int, error) {
	log := r.Log
	if log == nil {
		log = slog.Default()
	}
	if r.DryRun {
		return r.survey(ctx, stage, log)
	}

	applied := 0
	for _, s := range stage {
		done, err := s.Check(ctx)
		if err != nil {
			return applied, fmt.Errorf("check %s: %w", s.Name, err)
		}
		if done {
			log.Info("step already done, skipping", "step", s.Name)
			continue
		}
		log.Info("applying step", "step", s.Name)
		if err := s.Apply(ctx); err != nil {
			return applied, fmt.Errorf("apply %s: %w", s.Name, err)
		}
		applied++
		log.Info("step applied", "step", s.Name)
	}
	return applied, nil
}

// survey is the dry-run path: report each step's state, apply nothing.
func (r *Runner) survey(ctx context.Context, stage []Step, log *slog.Logger) (int, error) {
	out := r.Out
	if out == nil {
		out = io.Discard
	}
	pending := 0
	for _, s := range stage {
		done, err := s.Check(ctx)
		var state string
		switch {
		case err != nil:
			state = fmt.Sprintf("unknown (check failed: %v)", err)
			log.Warn("dry-run check failed", "step", s.Name, "err", err)
		case done:
			state = "done"
		default:
			state = "pending"
			pending++
		}
		if _, err := fmt.Fprintf(out, "%-10s %s\n", state, s.Name); err != nil {
			return pending, fmt.Errorf("write dry-run report: %w", err)
		}
	}
	if _, err := fmt.Fprintf(out, "\ndry-run: %d of %d steps pending, nothing applied\n",
		pending, len(stage)); err != nil {
		return pending, fmt.Errorf("write dry-run report: %w", err)
	}
	return pending, nil
}
