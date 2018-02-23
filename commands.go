package music

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/bwmarrin/discordgo"
	"github.com/jeffreymkabot/musicbot/plugins"
)

type command struct {
	name            string
	alias           []string
	usage           string // should at least have usage
	short           string
	long            string
	ownerOnly       bool
	restrictChannel bool
	ack             string // must be an emoji, used to react on success
	shortcut        string // must be an emoji, users can react to the status message to invoke this command
	run             func(*guildService, GuildEvent, []string) error
}

// determine which command to run and with what arguments.
// bool return will be false for no matching command, like with accessing maps
// e.g. cmd, args, ok := parseCommand(argv)
func parseCommand(args []string) (command, []string, bool) {
	if len(args) == 0 {
		return command{}, args, false
	}
	// if arg[0] resembles a url try the domain as a command name/alias and args are arg[0:]
	// else try arg[0] is a command name/alias and args are arg[1:]
	candidateCmd := ""
	if strings.HasPrefix(args[0], "http") {
		if u, err := url.Parse(args[0]); err == nil {
			candidateCmd = strings.ToLower(domainFrom(u.Hostname()))
			if cmd, ok := commandByNameOrAlias(candidateCmd); ok {
				return cmd, args, true
			}
		}
	}
	candidateCmd = strings.ToLower(args[0])
	if cmd, ok := commandByNameOrAlias(candidateCmd); ok {
		return cmd, args[1:], true
	}
	// TODO if still none try to see if streamlink will handle the url and synthesize a command
	return command{}, args, false
}

// get "example" in example, example., example.com, www.example.com, www.system.example.com
func domainFrom(hostname string) string {
	parts := strings.Split(hostname, ".")
	if len(parts) < 3 {
		return parts[0]
	}
	return parts[len(parts)-2]
}

func commandByNameOrAlias(candidate string) (command, bool) {
	for _, cmd := range commands {
		if candidate == cmd.name {
			return cmd, true
		}
		for _, alias := range cmd.alias {
			if candidate == alias {
				return cmd, true
			}
		}
	}
	return command{}, false
}

var commands []command

// avoid initialization loop since help command refers to this list
func init() {
	commands = []command{
		help,
		youtube,
		soundcloud,
		twitch,
		bandcamp,
		pause,
		skip,
		clear,
		reconnect,
		get,
		set,
		setListen,
		unsetListen,
	}
}

var youtube = command{
	name:            "youtube",
	alias:           []string{"yt", "youtu"},
	usage:           "youtube [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(gsvc *guildService, evt GuildEvent, args []string) error {
		if len(args) == 0 {
			return errors.New("video please")
		}
		return gsvc.enqueue(plugins.Youtube{}, args[0], evt.Channel.ID)
	},
}

var soundcloud = command{
	name:            "soundcloud",
	alias:           []string{"sc", "snd"},
	usage:           "soundcloud [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(gsvc *guildService, evt GuildEvent, args []string) error {
		if len(args) == 0 {
			return errors.New("track please")
		}
		return gsvc.enqueue(plugins.Soundcloud{gsvc.soundcloud}, args[0], evt.Channel.ID)
	},
}

var bandcamp = command{
	name:            "bandcamp",
	alias:           []string{"bc"},
	usage:           "bandcamp [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(gsvc *guildService, evt GuildEvent, args []string) error {
		if len(args) == 0 {
			return errors.New("track please")
		}
		return gsvc.enqueue(plugins.Bandcamp{}, args[0], evt.Channel.ID)
	},
}

var twitch = command{
	name:            "twitch",
	usage:           "twitch [url]",
	restrictChannel: true,
	ack:             "☑",
	run: func(gsvc *guildService, evt GuildEvent, args []string) error {
		if len(args) == 0 {
			return errors.New("channel please")
		}
		return gsvc.enqueue(plugins.Twitch{}, args[0], evt.Channel.ID)
	},
}

var reconnect = command{
	name:            "reconnect",
	usage:           "reconnect",
	restrictChannel: true,
	ack:             "🆗",
	run: func(gsvc *guildService, evt GuildEvent, args []string) error {
		gsvc.reconnect()
		return nil
	},
}

var skip = command{
	name:            "skip",
	usage:           "skip",
	restrictChannel: true,
	shortcut:        "⏭",
	run: func(gsvc *guildService, evt GuildEvent, args []string) error {
		gsvc.player.Skip()
		return nil
	},
}

var pause = command{
	name:            "pause",
	alias:           []string{"p"},
	usage:           "pause",
	restrictChannel: true,
	shortcut:        "⏯",
	run: func(gsvc *guildService, evt GuildEvent, args []string) error {
		gsvc.player.Pause()
		return nil
	},
}

var clear = command{
	name:            "clear",
	alias:           []string{"cl"},
	usage:           "clear",
	restrictChannel: true,
	ack:             "🔘",
	run: func(gsvc *guildService, evt GuildEvent, args []string) error {
		gsvc.player.Clear()
		return nil
	},
}

var get = command{
	name:  "get",
	usage: "get [field]",
	run: func(gsvc *guildService, evt GuildEvent, args []string) error {
		if len(args) == 0 {
			return errors.New("field please")
		}
		fieldRe, err := regexp.Compile("(?i)" + args[0])
		if err != nil {
			return err
		}
		info := reflect.ValueOf(&gsvc.GuildInfo).Elem()
		infoType := info.Type()
		var fields []struct {
			name string
			val  interface{}
		}
		for i := 0; i < infoType.NumField(); i++ {
			fldName := infoType.Field(i).Name
			if fieldRe.MatchString(fldName) {
				val := info.Field(i).Interface()
				fields = append(fields, struct {
					name string
					val  interface{}
				}{fldName, val})
			}
		}
		if len(fields) == 0 {
			return errors.New("field not found")
		}
		buf := &bytes.Buffer{}
		for _, fld := range fields {
			fmt.Fprintf(buf, "`%v: %v`\n", fld.name, fld.val)
		}
		gsvc.discord.ChannelMessageSend(evt.Channel.ID, buf.String())
		return nil
	},
}

var set = command{
	name:  "set",
	usage: "set [field] [value]",
	ack:   "🆗",
	run: func(gsvc *guildService, evt GuildEvent, args []string) error {
		if len(args) < 2 {
			return errors.New("field and value please")
		}
		info := reflect.ValueOf(&gsvc.GuildInfo).Elem()
		infoType := info.Type()
		for i := 0; i < infoType.NumField(); i++ {
			fldName := infoType.Field(i).Name
			fldType := infoType.Field(i).Type.Kind()
			// TODO support non string fields e.g. loudness is a float64
			if fldType == reflect.String && strings.ToLower(fldName) == strings.ToLower(args[0]) {
				info.Field(i).SetString(args[1])
				gsvc.save()
				return nil
			}
		}
		return errors.New("settable field not found")
	},
}

var setListen = command{
	name:  "whitelist",
	usage: "whitelist",
	ack:   "🆗",
	run: func(gsvc *guildService, evt GuildEvent, args []string) error {
		textChannelID := evt.Channel.ID
		if textChannelID == "" {
			return errors.New("channel please")
		}
		if !contains(gsvc.ListenChannels, textChannelID) {
			gsvc.ListenChannels = append(gsvc.ListenChannels, textChannelID)
		}
		return gsvc.save()
	},
}

var unsetListen = command{
	name:  "unwhitelist",
	usage: "unwhitelist",
	ack:   "🆗",
	run: func(gsvc *guildService, evt GuildEvent, args []string) error {
		textChannelID := evt.Channel.ID
		if textChannelID == "" {
			return errors.New("channel please")
		}
		for i, ch := range gsvc.ListenChannels {
			if ch == textChannelID {
				gsvc.ListenChannels = append(gsvc.ListenChannels[:i], gsvc.ListenChannels[i+1:]...)
			}
		}
		return gsvc.save()
	},
}

var help = command{
	name:  "help",
	alias: []string{"h"},
	usage: "help [command name]",
	ack:   "📬",
	run: func(gsvc *guildService, evt GuildEvent, args []string) error {
		// help gets whispered to the user
		dmChannelID := ""
		if evt.Channel.Type == discordgo.ChannelTypeDM || evt.Channel.Type == discordgo.ChannelTypeGroupDM {
			dmChannelID = evt.Channel.ID
		} else if channel, err := gsvc.discord.UserChannelCreate(evt.Message.Author.ID); err == nil {
			dmChannelID = channel.ID
		} else {
			return err
		}

		if len(args) > 0 && args[0] != "" {
			if cmd, ok := commandByNameOrAlias(args[0]); ok {
				embed := helpForCommand(cmd)
				_, err := gsvc.discord.ChannelMessageSendEmbed(dmChannelID, embed)
				return err
			}
		}

		embed := helpForCommandList(commands)
		_, err := gsvc.discord.ChannelMessageSendEmbed(dmChannelID, embed)
		return err
	},
}

func helpForCommand(cmd command) *discordgo.MessageEmbed {
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

func helpForCommandList(commands []command) *discordgo.MessageEmbed {
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
