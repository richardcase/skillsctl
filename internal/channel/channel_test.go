package channel

import (
	"errors"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
)

// Every channel ships today, so nothing in the CLI can reach ErrUnsupported.
// The mechanism still has to work: a source parses long before its channel is
// built, and the difference between "that is not a source" and "not yet" is
// what a future channel will land on.
func TestForReportsAChannelWithNoImplementation(t *testing.T) {
	empty := Registry{}

	for _, c := range []source.Channel{source.ChannelGit, source.ChannelPlugin, source.ChannelLocal} {
		t.Run(string(c), func(t *testing.T) {
			_, err := empty.For(c)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("error = %v, want it to wrap ErrUnsupported", err)
			}
			if !strings.Contains(err.Error(), string(c)) {
				t.Errorf("error = %v, want it to name the channel", err)
			}
		})
	}
}

func TestForResolvesEachRegisteredChannel(t *testing.T) {
	plugin, _ := newPluginChannel()
	reg := Registry{
		Git:    NewGit(nil, nil),
		Plugin: plugin,
		Local:  NewLocal(nil),
	}

	for _, tc := range []struct {
		channel source.Channel
		want    Ownership
	}{
		{source.ChannelGit, StoreOwned},
		{source.ChannelPlugin, AgentOwned},
		{source.ChannelLocal, UserOwned},
	} {
		t.Run(string(tc.channel), func(t *testing.T) {
			ch, err := reg.For(tc.channel)
			if err != nil {
				t.Fatalf("For(%s): %v", tc.channel, err)
			}
			if got := ch.Ownership(); got != tc.want {
				t.Errorf("Ownership = %v, want %v", got, tc.want)
			}
		})
	}
}

// list has to describe what is on disk even when it cannot explain it, because
// a receipt is a fact and refusing to render one helps nobody.
func TestRegistryAgentsFallsBackToTheLinksOfAnUnknownChannel(t *testing.T) {
	r := &state.Receipt{
		Name:    "orphan",
		Channel: "some-future-channel",
		Links:   []state.Link{{Target: "claude", Path: "/agents/claude/skills/orphan"}},
	}

	got := Registry{}.Agents(r)
	if len(got) != 1 || got[0] != "claude" {
		t.Errorf("Agents = %v, want the receipt's own links", got)
	}
}
