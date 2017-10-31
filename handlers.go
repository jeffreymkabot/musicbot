package music

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func onReady(b *Bot) func(*discordgo.Session, *discordgo.Ready) {
	return func(session *discordgo.Session, ready *discordgo.Ready) {
		log.Printf("ready %#v", ready)
		for _, g := range ready.Guilds {
			if !g.Unavailable {
				gc := &discordgo.GuildCreate{Guild: g}
				onGuildCreate(b)(session, gc)
			}
		}
		session.UpdateStatus(0, fmt.Sprintf("%s help", defaultPrefix))
	}
}

func onGuildCreate(b *Bot) func(*discordgo.Session, *discordgo.GuildCreate) {
	return func(session *discordgo.Session, gc *discordgo.GuildCreate) {
		log.Printf("guild create %v", gc.Guild.ID)

		// will cleanup existing guild structure if exists
		// e.g. disconnected, kicked and reinvited
		b.addGuild(gc.Guild)
	}
}

// worth noting that discordgo event handlers are executed in new goroutines,
// hence all command invocations are necessarily async
func onMessageCreate(b *Bot) func(*discordgo.Session, *discordgo.MessageCreate) {
	return func(session *discordgo.Session, mc *discordgo.MessageCreate) {
		if mc.Author.Bot {
			return
		}

		textChannel, err := session.State.Channel(mc.ChannelID)
		if err != nil {
			return
		}

		switch textChannel.Type {
		case discordgo.ChannelTypeGuildText:
			onGuildMessage(b, mc.Message, textChannel)
		case discordgo.ChannelTypeGroupDM:
			fallthrough
		case discordgo.ChannelTypeDM:
			onDirectMessage(b, mc.Message, textChannel)
		}
	}
}

func onDirectMessage(b *Bot, msg *discordgo.Message, ch *discordgo.Channel) {
	args := strings.Fields(strings.TrimPrefix(msg.Content, defaultPrefix))
	if len(args) > 0 {
		if commandByNameOrAlias(b.commands, strings.ToLower(args[0])) == help {
			args = args[1:]
		}
	}

	env := &environment{
		message: msg,
		channel: ch,
	}
	help.run(b, env, nil, args)
}

func onGuildMessage(b *Bot, msg *discordgo.Message, ch *discordgo.Channel) {
	b.mu.RLock()
	gu, ok := b.guilds[ch.GuildID]
	b.mu.RUnlock()
	if !ok {
		return
	}

	prefix := defaultPrefix
	gu.mu.RLock()
	if gu.Prefix != "" {
		prefix = gu.Prefix
	}
	gu.mu.RUnlock()

	if !strings.HasPrefix(msg.Content, prefix) {
		return
	}

	args := strings.Fields(strings.TrimPrefix(msg.Content, prefix))
	if len(args) == 0 {
		return
	}

	// if arg[0] resembles a url, try the url domain as the command name/alias and args are arg[0:]
	// else, arg[0] is the command name/alias and args are arg[1:]
	var candidateCmd string
	if strings.HasPrefix(args[0], "http") {
		if u, err := url.Parse(args[0]); err == nil {
			candidateCmd = strings.ToLower(domainFrom(u.Hostname()))
		}
	}
	if candidateCmd == "" {
		candidateCmd = strings.ToLower(args[0])
		args = args[1:]
	}
	log.Printf("candidate cmd %v", candidateCmd)

	cmd := commandByNameOrAlias(b.commands, candidateCmd)
	if cmd == nil {
		return
	}

	env := &environment{
		message: msg,
		channel: ch,
	}
	b.exec(cmd, env, gu, args)
}

// get "example" in example, example., example.com, www.example.com, www.system.example.com
func domainFrom(hostname string) string {
	parts := strings.Split(hostname, ".")
	if len(parts) < 3 {
		return parts[0]
	}
	return parts[len(parts)-2]
}

func commandByNameOrAlias(commands []*command, candidate string) *command {
	for _, cmd := range commands {
		if candidate == cmd.name {
			return cmd
		}
		for _, alias := range cmd.alias {
			if candidate == alias {
				return cmd
			}
		}
	}
	return nil
}
