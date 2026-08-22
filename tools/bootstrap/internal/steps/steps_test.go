package steps

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeWorld tracks which steps have "happened"; Check consults it and
// Apply mutates it, mimicking the config-vs-world convergence model.
type fakeWorld struct {
	done    map[string]bool
	applies []string
	checks  []string
}

func (w *fakeWorld) step(name string) Step {
	return Step{
		Name: name,
		Check: func(context.Context) (bool, error) {
			w.checks = append(w.checks, name)
			return w.done[name], nil
		},
		Apply: func(context.Context) error {
			w.applies = append(w.applies, name)
			w.done[name] = true
			return nil
		},
	}
}

func newFakeWorld(doneSteps ...string) *fakeWorld {
	w := &fakeWorld{done: make(map[string]bool)}
	for _, s := range doneSteps {
		w.done[s] = true
	}
	return w
}

func TestRunnerRun(t *testing.T) {
	tests := []struct {
		name        string
		alreadyDone []string
		wantApplies []string
	}{
		{
			name:        "fresh world applies everything in order",
			wantApplies: []string{"one", "two", "three"},
		},
		{
			name:        "interrupted world applies only the remainder",
			alreadyDone: []string{"one"},
			wantApplies: []string{"two", "three"},
		},
		{
			name:        "converged world applies nothing",
			alreadyDone: []string{"one", "two", "three"},
			wantApplies: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newFakeWorld(tt.alreadyDone...)
			stage := []Step{w.step("one"), w.step("two"), w.step("three")}

			var r Runner
			res, err := r.Run(context.Background(), stage)
			if err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			if res.Applied != len(tt.wantApplies) {
				t.Errorf("Run() applied = %d, want %d", res.Applied, len(tt.wantApplies))
			}
			if got, want := strings.Join(w.applies, ","), strings.Join(tt.wantApplies, ","); got != want {
				t.Errorf("applies = %q, want %q", got, want)
			}
		})
	}
}

func TestRunnerRunErrors(t *testing.T) {
	sentinel := errors.New("boom")

	t.Run("check failure stops the run wrapped with the step name", func(t *testing.T) {
		w := newFakeWorld()
		stage := []Step{
			w.step("one"),
			{
				Name:  "broken",
				Check: func(context.Context) (bool, error) { return false, sentinel },
			},
			w.step("never"),
		}
		var r Runner
		_, err := r.Run(context.Background(), stage)
		if !errors.Is(err, sentinel) {
			t.Fatalf("Run() error = %v, want wrapped sentinel", err)
		}
		if !strings.Contains(err.Error(), "check broken") {
			t.Errorf("Run() error = %q, want it to name the step", err)
		}
		if len(w.applies) != 1 || w.applies[0] != "one" {
			t.Errorf("applies = %v, want only the step before the failure", w.applies)
		}
	})

	t.Run("apply failure stops the run wrapped with the step name", func(t *testing.T) {
		w := newFakeWorld()
		stage := []Step{
			{
				Name:  "broken",
				Check: func(context.Context) (bool, error) { return false, nil },
				Apply: func(context.Context) error { return sentinel },
			},
			w.step("never"),
		}
		var r Runner
		_, err := r.Run(context.Background(), stage)
		if !errors.Is(err, sentinel) {
			t.Fatalf("Run() error = %v, want wrapped sentinel", err)
		}
		if !strings.Contains(err.Error(), "apply broken") {
			t.Errorf("Run() error = %q, want it to name the step", err)
		}
		if len(w.applies) != 0 {
			t.Errorf("applies = %v, want none after the failure", w.applies)
		}
	})
}

func TestRunnerDryRun(t *testing.T) {
	w := newFakeWorld("one")
	stage := []Step{
		w.step("one"),
		w.step("two"),
		{
			Name:  "unknowable",
			Check: func(context.Context) (bool, error) { return false, errors.New("api down") },
		},
	}

	var out strings.Builder
	r := Runner{DryRun: true, Out: &out}
	res, err := r.Run(context.Background(), stage)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if len(w.applies) != 0 {
		t.Fatalf("dry-run applied steps: %v", w.applies)
	}
	if res.Pending != 1 {
		t.Errorf("Run() pending = %d, want 1", res.Pending)
	}
	if res.Applied != 0 {
		t.Errorf("Run() applied = %d, want 0 in dry-run", res.Applied)
	}
	report := out.String()
	for _, want := range []string{
		"done", "one",
		"pending", "two",
		"unknown (check failed: api down)", "unknowable",
		"1 of 3 steps pending, nothing applied",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("dry-run report missing %q:\n%s", want, report)
		}
	}
	// Every step was surveyed even after the failed check.
	if len(w.checks) != 2 {
		t.Errorf("checks = %v, want both fake steps checked", w.checks)
	}
}
