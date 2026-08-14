package cli

import (
	"context"
	"fmt"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
)

// newRunner supplies plan.Executor.Run, which is how a plan's Exec ops reach a
// binary. nil means os/exec; tests replace this to keep a unit test from
// shelling out.
var newRunner = func() func(context.Context, []string) error { return nil }

// env is the resolved environment a command runs against.
type env struct {
	store *store.Store
	cfg   target.Config
}

func newEnv() (*env, error) {
	root, err := store.Home()
	if err != nil {
		return nil, err
	}
	cfgPath, err := target.ConfigPath()
	if err != nil {
		return nil, err
	}
	cfg, err := target.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	return &env{store: store.New(root), cfg: cfg}, nil
}

// targets resolves the -a flag, defaulting to every present agent.
func (e *env) targets(names []string) ([]target.Target, error) {
	if len(names) > 0 {
		return e.cfg.Select(names)
	}
	present := e.cfg.Present()
	if len(present) == 0 {
		return nil, fmt.Errorf("no agent directories found: create one (for example ~/.claude) or configure targets")
	}
	return present, nil
}

// openState acquires the receipts database.
func (e *env) openState() (*state.Handle, error) {
	return state.Open(e.store.StatePath())
}

// channels is the registry every command dispatches through. It is built per
// command rather than shared, because a channel is bound to the store and the
// binaries the command is running against.
func (e *env) channels() channel.Registry {
	return channel.Registry{
		Git: channel.NewGit(e.store, gitx.New()),
	}
}
