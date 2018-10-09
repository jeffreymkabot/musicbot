package musicbot

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestCheckMessageEvent(t *testing.T) {
	musicbot := &discordgo.User{
		ID: "musicbot",
	}
	someoneelse := &discordgo.User{
		ID: "someoneelse",
	}

	// setup state
	state := discordgo.NewState()
	state.Ready = discordgo.Ready{
		User: musicbot,
	}
	gsvc := &GuildService{
		discord: &discordgo.Session{
			State: state,
		},
		GuildConfig: GuildConfig{
			Prefix: "xx",
		},
	}

	message := &discordgo.Message{
		ID:        "message",
		ChannelID: "channel",
		GuildID:   "guild",
	}
	evt := MessageEvent{
		Message: message,
	}

	type vary struct {
		Content  string
		Mentions []*discordgo.User
	}
	type expected struct {
		arg string
		ok  bool
	}
	type testCase struct {
		description string
		vary        vary
		expected    expected
	}

	cases := []testCase{
		{
			description: "empty",
			vary: vary{
				Content: "",
			},
			expected: expected{
				ok: false,
			},
		},
		{
			description: "arbitrary message",
			vary: vary{
				Content: "lorem ipsum dolor sit amet",
			},
			expected: expected{
				ok: false,
			},
		},
		{
			description: "global prefix",
			vary: vary{
				Content: DefaultCommandPrefix + " hello world    ",
			},
			expected: expected{
				ok:  true,
				arg: "hello world",
			},
		},
		{
			description: "guild prefix",
			vary: vary{
				Content: gsvc.Prefix + " abc 123",
			},
			expected: expected{
				ok:  true,
				arg: "abc 123",
			},
		},
		{
			description: "mentions musicbot at the beginning",
			vary: vary{
				Content:  musicbot.Mention() + " hello world",
				Mentions: []*discordgo.User{musicbot},
			},
			expected: expected{
				ok: true,
				// should remove all mentions from arg string
				arg: "hello world",
			},
		},
		{
			description: "mentions musicbot in the middle",
			vary: vary{
				Content:  "hello " + musicbot.Mention() + " world",
				Mentions: []*discordgo.User{musicbot},
			},
			expected: expected{
				ok: false,
			},
		},
		{
			description: "mentions musicbot at the end",
			vary: vary{
				Content:  "hello world " + musicbot.Mention(),
				Mentions: []*discordgo.User{musicbot},
			},
			expected: expected{
				ok: false,
			},
		},
		{
			description: "mentions someone else",
			vary: vary{
				Content:  "hello world",
				Mentions: []*discordgo.User{someoneelse},
			},
			expected: expected{
				ok: false,
			},
		},
		{
			description: "mentions musicbot at the beginning, then mentions someone else",
			vary: vary{
				Content:  musicbot.Mention() + " hello " + someoneelse.Mention(),
				Mentions: []*discordgo.User{musicbot, someoneelse},
			},
			expected: expected{
				ok: true,
				// should remove all mentions from arg string
				arg: "hello " + someoneelse.Mention(),
			},
		},
	}

	for _, c := range cases {
		// mutate message by reference
		message.Mentions = c.vary.Mentions
		message.Content = c.vary.Content
		arg, ok := gsvc.checkMessageEvent(evt)
		if ok != c.expected.ok {
			t.Error(c.description)
			continue
		}
		if ok && arg != c.expected.arg {
			t.Error(c.description + "\nexpected arg: " + c.expected.arg + "\ngot: " + arg)
		}
	}
}
