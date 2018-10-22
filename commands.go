package musicbot

import (
	"bytes"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/bwmarrin/discordgo"
	"github.com/fatih/structs"
	"github.com/jeffreymkabot/musicbot/plugins"
	"github.com/pkg/errors"
)

type serviceFunc func(*GuildService, MessageEvent, []string) error

type command struct {
	name  string
	alias []string
	usage string // should at least have usage
	short string
	long  string
	// only run for the owner of the guild
	ownerOnly bool
	// only run in a guild's whitelisted channels
	restrictChannel bool
	ack             string // must be an emoji, used to react on success
	shortcut        string // must be an emoji, users can react to the status message to invoke this command
	run             serviceFunc
}

// determine what to do in response to the provided arguments
// bool return will be false for no match
// e.g. cmd, args, ok := matchCommand(argv)
func matchCommand(available []command, arg string) (command, []string, bool) {
	argv := strings.Fields(arg)
	if len(argv) == 0 {
		return command{}, nil, false
	}

	candidateCmd := strings.ToLower(argv[0])
	if cmd, ok := commandByNameOrAlias(available, candidateCmd); ok {
		return cmd, argv[1:], true
	}

	return command{}, nil, false
}

// determine a plugin that can handle the provided arguments
// bool return will be false for no match
func matchPlugin(plugins []plugins.Plugin, arg string) (serviceFunc, bool) {
	if arg == "" {
		return nil, false
	}

	for _, pl := range plugins {
		if pl.CanHandle(arg) {
			return runPlugin(pl, arg), true
		}
	}

	return nil, false
}

func runPlugin(plugin plugins.Plugin, arg string) serviceFunc {
	return func(gsvc *GuildService, evt MessageEvent, _ []string) error {
		gsvc.discord.MessageReactionAdd(evt.Message.ChannelID, evt.Message.ID, "🔎")
		defer gsvc.discord.MessageReactionRemove(evt.Message.ChannelID, evt.Message.ID, "🔎", "@me")

		var md plugins.Metadata
		var err error
		var vp plugins.VideoPlugin
		var ok bool

		// resolve video plugin if supported
		// fall back to regular plugin if not supported, or error resolving video
		if vp, ok = plugin.(plugins.VideoPlugin); ok {
			md, err = vp.ResolveWithVideo(arg)
		}
		if !ok || err != nil {
			md, err = plugin.Resolve(arg)
		}
		if err != nil {
			return errors.Wrap(err, "failed to resolve openable stream")
		}
		return gsvc.player.Put(evt, gsvc.MusicChannel, md, gsvc.Loudness)
	}
}

func commandByNameOrAlias(commands []command, candidate string) (command, bool) {
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

var reconnect = command{
	name:            "reconnect",
	usage:           "reconnect",
	long:            "Restart the music player.  This will empty the playlist.",
	restrictChannel: true,
	ack:             "🆗",
	run: func(gsvc *GuildService, evt MessageEvent, args []string) error {
		gsvc.player.Close()
		// idle in the music channel
		gsvc.player = NewGuildPlayer(
			gsvc.guildID,
			gsvc.discord,
			gsvc.MusicChannel,
			commandShortcuts(gsvc.commands),
		)
		return nil
	},
}

var skip = command{
	name:            "skip",
	usage:           "skip",
	long:            "Skip the currently playing song.",
	restrictChannel: true,
	shortcut:        "⏭",
	run: func(gsvc *GuildService, evt MessageEvent, args []string) error {
		gsvc.player.Skip()
		return nil
	},
}

var pause = command{
	name:            "pause",
	alias:           []string{"p"},
	usage:           "pause",
	long:            "Pause/unpause the currently playing song.",
	restrictChannel: true,
	shortcut:        "⏯",
	run: func(gsvc *GuildService, evt MessageEvent, args []string) error {
		gsvc.player.Pause()
		return nil
	},
}

var clear = command{
	name:            "clear",
	alias:           []string{"cl"},
	usage:           "clear",
	long:            "Clear the playlist.",
	restrictChannel: true,
	ack:             "🔘",
	run: func(gsvc *GuildService, evt MessageEvent, args []string) error {
		gsvc.player.Clear()
		return nil
	},
}

var requeue = command{
	name:            "requeue",
	alias:           []string{"rq"},
	usage:           "requeue",
	long:            "Requeue the currently playing song.",
	restrictChannel: true,
	shortcut:        "🔂",
	ack:             "☑",
	run: func(gsvc *GuildService, evt MessageEvent, args []string) error {
		play, ok := gsvc.player.NowPlaying()
		if !ok {
			return errors.New("nothing playing")
		}
		return gsvc.player.Put(evt, gsvc.MusicChannel, play.Metadata, gsvc.Loudness)
	},
}

var playlist = command{
	name:            "playlist",
	alias:           []string{"list", "ls", "lst"},
	usage:           "playlist",
	long:            "List any queued songs.",
	restrictChannel: true,
	run: func(gsvc *GuildService, evt MessageEvent, args []string) error {
		playlistString := strings.Join(gsvc.player.Playlist(), "\n")
		gsvc.discord.ChannelMessageSend(evt.Message.ID, "```\n"+playlistString+"\n```")
		return nil
	},
}

var get = command{
	name:  "get",
	usage: "get [field]",
	long:  "Get preferences saved for this guild.  Supports regular expressions.\nE.g., `get .*` to get all preferences.",
	run: func(gsvc *GuildService, evt MessageEvent, args []string) error {
		if len(args) == 0 {
			return errors.New("field please")
		}
		fieldRe, err := regexp.Compile("(?i)" + args[0])
		if err != nil {
			return err
		}
		info := reflect.ValueOf(&gsvc.GuildConfig).Elem()
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
		gsvc.discord.ChannelMessageSend(evt.Message.ChannelID, buf.String())
		return nil
	},
}

// omit value to zero the field
// deferred call to get.run serves as ack
var set = command{
	name:  "set",
	usage: "set [field] [value]",
	long:  "Set preferences for this guild.  Omit [value] to empty the preference.",
	run: func(gsvc *GuildService, evt MessageEvent, args []string) error {
		if len(args) == 0 {
			return errors.New("field please")
		}
		defer get.run(gsvc, evt, args)
		defer gsvc.store.Put(gsvc.guildID, gsvc.GuildConfig)
		info := structs.New(&gsvc.GuildConfig)
		for _, fld := range info.Fields() {
			if strings.ToLower(fld.Name()) == strings.ToLower(args[0]) {
				if len(args) == 1 {
					return fld.Zero()
				}
				val, err := resolveValue(fld.Kind(), args[1])
				if err != nil {
					return err
				}
				return fld.Set(val)
			}
		}
		return errors.New("field not found")
	},
}

func resolveValue(kind reflect.Kind, arg string) (val interface{}, err error) {
	switch kind {
	case reflect.String:
		val = arg
	case reflect.Bool:
		val, err = strconv.ParseBool(arg)
	case reflect.Int:
		val, err = strconv.Atoi(arg)
	case reflect.Float64:
		val, err = strconv.ParseFloat(arg, 64)
	default:
		val, err = nil, errors.New("unsupported type")
	}
	return
}

var setPlayback = command{
	name:  "playback",
	usage: "playback [detect|here]",
	long: "Set the music playback channel for the guild." +
		"\n`playback` or `playback detect` set it to a voice channel with a name that contains the word `music`." +
		"\n`playback here` will set it to the voice channel you are in.",
	ack: "🆗",
	run: func(gsvc *GuildService, evt MessageEvent, args []string) error {
		guild, err := gsvc.discord.State.Guild(gsvc.guildID)
		if err != nil {
			guild, err = gsvc.discord.Guild(gsvc.guildID)
		}
		if err != nil {
			return err
		}

		channelID := ""
		if len(args) == 0 || strings.ToLower(args[0]) == "detect" {
			channelID = detectMusicChannel(guild)
		} else if strings.ToLower(args[0]) == "here" {
			channelID = detectUserVoiceChannel(guild, evt.Message.Author.ID)
		}
		if channelID == "" {
			return errors.New("couldn't detect a voice channel")
		}

		gsvc.MusicChannel = channelID
		return nil
	},
}

var setListen = command{
	name:  "whitelist",
	usage: "whitelist",
	ack:   "🆗",
	run: func(gsvc *GuildService, evt MessageEvent, args []string) error {
		textChannelID := evt.Message.ChannelID
		if textChannelID == "" {
			return errors.New("channel please")
		}
		if !contains(gsvc.ListenChannels, textChannelID) {
			gsvc.ListenChannels = append(gsvc.ListenChannels, textChannelID)
		}
		return gsvc.store.Put(gsvc.guildID, gsvc.GuildConfig)
	},
}

var unsetListen = command{
	name:  "unwhitelist",
	usage: "unwhitelist",
	ack:   "🆗",
	run: func(gsvc *GuildService, evt MessageEvent, args []string) error {
		textChannelID := evt.Message.ChannelID
		if textChannelID == "" {
			return errors.New("channel please")
		}
		for i, ch := range gsvc.ListenChannels {
			if ch == textChannelID {
				gsvc.ListenChannels = append(gsvc.ListenChannels[:i], gsvc.ListenChannels[i+1:]...)
			}
		}
		return gsvc.store.Put(gsvc.guildID, gsvc.GuildConfig)
	},
}

var help = command{
	name:     "help",
	alias:    []string{"h"},
	usage:    "help [command name]",
	long:     "Get help about features and commands.",
	shortcut: "❔",
	ack:      "📬",
	run: func(gsvc *GuildService, evt MessageEvent, args []string) error {
		// help gets whispered to the user
		dmChannelID := ""
		evtChannel, err := gsvc.discord.State.Channel(evt.Message.ChannelID)
		if err == nil && evtChannel.Type == discordgo.ChannelTypeDM || evtChannel.Type == discordgo.ChannelTypeGroupDM {
			dmChannelID = evt.Message.ChannelID
		} else if channel, err := gsvc.discord.UserChannelCreate(evt.Message.Author.ID); err == nil {
			dmChannelID = channel.ID
		} else {
			return err
		}

		if len(args) > 0 && args[0] != "" {
			if cmd, ok := commandByNameOrAlias(gsvc.commands, args[0]); ok {
				embed := helpForCommand(cmd)
				_, err := gsvc.discord.ChannelMessageSendEmbed(dmChannelID, embed)
				return err
			}
		}

		embed := helpForCommandList(gsvc.commands)
		_, err = gsvc.discord.ChannelMessageSendEmbed(dmChannelID, embed)
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

var helpDesc = fmt.Sprintf(
	"Queue a song using `%s [url]`.\nAll commands start with `%s`.\n\nTo get more help about a command, whisper me the name or use `%s help [name]`.",
	DefaultCommandPrefix, DefaultCommandPrefix, DefaultCommandPrefix,
)

// TODO update help instructions to reflect supported plugins
func helpForCommandList(commands []command) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{}
	embed.Title = "help"
	embed.Description = helpDesc
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

// help command shortcut always appears at end
func commandShortcuts(commands []command) (sc []string) {
	for _, cmd := range commands {
		if cmd.name != help.name && cmd.shortcut != "" {
			sc = append(sc, cmd.shortcut)
		}
	}
	if help.shortcut != "" {
		sc = append(sc, help.shortcut)
	}
	return
}
