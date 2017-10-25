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
		session.UpdateStatus(0, fmt.Sprintf("%s yt; %s sc; %s skip; %s pause", defaultPrefix, defaultPrefix, defaultPrefix, defaultPrefix))
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
		textChannel, err := session.State.Channel(mc.ChannelID)
		if err != nil || textChannel.GuildID == "" {
			return
		}

		b.mu.RLock()
		gu, ok := b.guilds[textChannel.GuildID]
		b.mu.RUnlock()
		if !ok {
			return
		}

		prefix := defaultPrefix
		if gu.Prefix != "" {
			prefix = gu.Prefix
		}

		if !strings.HasPrefix(mc.Content, prefix) {
			return
		}

		args := strings.Fields(strings.TrimPrefix(mc.Content, prefix))
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

		b.exec(cmd, gu, mc.Author.ID, mc.ID, textChannel.ID, args)
	}
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
