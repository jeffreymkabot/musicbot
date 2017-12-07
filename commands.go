package music

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"text/tabwriter"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"
	"github.com/jeffreymkabot/musicbot/plugins"
)

var ErrGuildCmd = errors.New("call this command in a guild")

// describe the context of a command invocation
type environment struct {
	message *discordgo.Message
	channel *discordgo.Channel
}

type command struct {
	name            string
	alias           []string
	usage           string // should at least have usage
	short           string
	long            string
	ownerOnly       bool
	restrictChannel bool
	ack             string // must be an emoji, used to react on success
	run             func(*Bot, *environment, *guild, []string) error
}

func helpWithCommand(cmd *command) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{}
	embed.Title = cmd.name
	embed.Fields = []*discordgo.MessageEmbedField{
		&discordgo.MessageEmbedField{
			Name:  "Usage",
			Value: fmt.Sprintf("`%s`", cmd.usage),
		},
	}
	if cmd.long != "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  "Description",
			Value: cmd.long,
		})
	}
	if len(cmd.alias) > 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  "Aliases",
			Value: fmt.Sprintf("`%s`", strings.Join(cmd.alias, "`, `")),
		})
	}
	if cmd.restrictChannel {
		embed.Footer = &discordgo.MessageEmbedFooter{
			Text: "This command will only run in whitelisted channels (see whitelist).",
		}
	}
	return embed
}

func helpWithCommandList(commands []*command) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{}
	embed.Title = "help"
	embed.Description = "Command can be inferred from a url if the url's domain is a command name or alias."
	buf := &bytes.Buffer{}
	w := tabwriter.NewWriter(buf, 4, 4, 0, '.', 0)
	for _, cmd := range commands {
		if !cmd.ownerOnly {
			aliasList := ""
			if len(cmd.alias) > 0 {
				aliasList = "`" + strings.Join(cmd.alias, "`, `") + "`"
			}
			restrictChannel := ""
			if cmd.restrictChannel {
				restrictChannel = "*"
			}
			fmt.Fprintf(w, "`%s%s..\t` %s\n", restrictChannel, cmd.name, aliasList)
		}
	}
	w.Flush()
	embed.Fields = []*discordgo.MessageEmbedField{
		&discordgo.MessageEmbedField{
			Name:  "Commands",
			Value: buf.String(),
		},
	}
	embed.Footer = &discordgo.MessageEmbedFooter{
		Text: "Commands with a * will only run in whitelisted channels.",
	}
	return embed
}

var help = &command{
	name:  "help",
	alias: []string{"h"},
	usage: "help [command name]",
	ack:   "📬",
	run: func(b *Bot, env *environment, gu *guild, args []string) error {
		// help gets whispered to the user
		var dm *discordgo.Channel
		var err error
		if env.channel.Type == discordgo.ChannelTypeDM || env.channel.Type == discordgo.ChannelTypeGroupDM {
			dm = env.channel
		} else if dm, err = b.session.UserChannelCreate(env.message.Author.ID); err != nil {
			return err
		}

		if len(args) > 0 && args[0] != "" {
			if cmd := commandByNameOrAlias(b.commands, args[0]); cmd != nil {
				embed := helpWithCommand(cmd)
				_, err = b.session.ChannelMessageSendEmbed(dm.ID, embed)
				return err
			}
		}

		embed := helpWithCommandList(b.commands)
		_, err = b.session.ChannelMessageSendEmbed(dm.ID, embed)
		return err
	},
}

var reconnect = &command{
	name:            "reconnect",
	usage:           "reconnect",
	restrictChannel: true,
	ack:             "🆗",
	run: func(b *Bot, env *environment, gu *guild, args []string) error {
		if gu == nil {
			return ErrGuildCmd
		}
		g, err := b.session.State.Guild(gu.guildID)
		if err == nil {
			b.addGuild(g)
		}
		return err
	},
}

var youtube = &command{
	name:            "youtube",
	alias:           []string{"yt", "youtu"},
	usage:           "youtube [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(b *Bot, env *environment, gu *guild, args []string) error {
		if gu == nil {
			return ErrGuildCmd
		}
		if len(args) == 0 {
			return errors.New("video please")
		}
		return b.enqueue(gu, &plugins.Youtube{}, args[0], env.channel.ID)
	},
}

var soundcloud = &command{
	name:            "soundcloud",
	alias:           []string{"sc", "snd"},
	usage:           "soundcloud [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(b *Bot, env *environment, gu *guild, args []string) error {
		if gu == nil {
			return ErrGuildCmd
		}
		if len(args) == 0 {
			return errors.New("track please")
		}
		return b.enqueue(gu, &plugins.Soundcloud{ClientID: b.soundcloud}, args[0], env.channel.ID)
	},
}

var bandcamp = &command{
	name:            "bandcamp",
	alias:           []string{"bc"},
	usage:           "bandcamp [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(b *Bot, env *environment, gu *guild, args []string) error {
		if gu == nil {
			return ErrGuildCmd
		}
		if len(args) == 0 {
			return errors.New("track please")
		}
		return b.enqueue(gu, &plugins.Bandcamp{}, args[0], env.channel.ID)
	},
}

var skip = &command{
	name:            "skip",
	usage:           "skip",
	restrictChannel: true,
	run: func(b *Bot, env *environment, gu *guild, args []string) error {
		if gu == nil {
			return ErrGuildCmd
		}
		if err := gu.play.Skip(); err != nil {
			log.Print("nop skip")
		}
		return nil
	},
}

var pause = &command{
	name:            "pause",
	alias:           []string{"p"},
	usage:           "pause",
	restrictChannel: true,
	run: func(b *Bot, env *environment, gu *guild, args []string) error {
		if gu == nil {
			return ErrGuildCmd
		}
		if err := gu.play.Pause(); err != nil {
			log.Print("nop pause")
		}
		return nil
	},
}

var clear = &command{
	name:            "clear",
	alias:           []string{"cl"},
	usage:           "clear",
	restrictChannel: true,
	ack:             "🔘",
	run: func(b *Bot, env *environment, gu *guild, args []string) error {
		if gu == nil {
			return ErrGuildCmd
		}
		return gu.play.Clear()
	},
}

var setPrefix = &command{
	name:  "prefix",
	usage: "prefix",
	run: func(b *Bot, env *environment, gu *guild, args []string) error {
		if gu == nil {
			return ErrGuildCmd
		}
		if len(args) == 0 || args[0] == "" {
			return errors.New("prefix please")
		}
		gu.mu.Lock()
		gu.Prefix = args[0]
		gu.mu.Unlock()
		// db
		return nil
	},
}

var setListen = &command{
	name:  "whitelist",
	usage: "whitelist",
	ack:   "🆗",
	run: func(b *Bot, env *environment, gu *guild, args []string) error {
		textChannelID := env.channel.ID
		if gu == nil {
			return ErrGuildCmd
		}
		if textChannelID == "" {
			return errors.New("channel please")
		}
		gu.mu.Lock()
		if !contains(gu.ListenChannels, textChannelID) {
			gu.ListenChannels = append(gu.ListenChannels, textChannelID)
		}
		gu.mu.Unlock()
		// db
		return b.db.Update(func(tx *bolt.Tx) error {
			bucket := tx.Bucket([]byte("guilds"))
			val, err := json.Marshal(gu.guildInfo)
			if err != nil {
				return err
			}
			return bucket.Put([]byte(gu.guildID), val)
		})
	},
}

var unsetListen = &command{
	name:  "unwhitelist",
	usage: "unwhitelist",
	ack:   "🆗",
	run: func(b *Bot, env *environment, gu *guild, args []string) error {
		textChannelID := env.channel.ID
		if gu == nil {
			return ErrGuildCmd
		}
		if textChannelID == "" {
			return errors.New("channel please")
		}
		gu.mu.Lock()
		for i, ch := range gu.ListenChannels {
			if ch == textChannelID {
				gu.ListenChannels = append(gu.ListenChannels[:i], gu.ListenChannels[i+1:]...)
			}
		}
		gu.mu.Unlock()
		// db
		return b.db.Update(func(tx *bolt.Tx) error {
			bucket := tx.Bucket([]byte("guilds"))
			val, err := json.Marshal(gu.guildInfo)
			if err != nil {
				return err
			}
			return bucket.Put([]byte(gu.guildID), val)
		})
	},
}
