package music

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jeffreymkabot/discordvoice"
	"github.com/jeffreymkabot/discordvoice/discordvoice"
	"github.com/jeffreymkabot/musicbot/plugins"
)

// ErrInvalidMusicChannel is emitted when the music channel configured for a guild is not a discord voice channel.
var ErrInvalidMusicChannel = errors.New("set a valid voice channel for music playback, then call reconnect")

// GuildPlayer streams audio to a voice channel in a guild.
type GuildPlayer interface {
	Put(evt GuildEvent, voiceChannelID string, md plugins.Metadata, loudness float64) error
	Skip()
	Pause()
	Clear()
	Close() error
	NowPlaying() (Play, bool)
}

// Play holds data related to the playback of an audio stream in a guild.
type Play struct {
	statusMessageChannelID string
	statusMessageID        string
	metadata               plugins.Metadata
}

type guildPlayer struct {
	guildID string
	discord *discordgo.Session
	*player.Player
	cmdShortcuts []string
	mu           sync.Mutex
	// TODO how to manage nowPlaying state in a reasonable way without mutex?
	// player state controlled by discordvoice#sender goroutine
	// guild state controlled by musicbot#Guild goroutine
	nowPlaying Play
}

// NewGuildPlayer creates a GuildPlayer resource for a discord guild.
// Existing open GuildPlayers for the same guild should be closed before making a new one to avoid interference.
func NewGuildPlayer(guildID string, discord *discordgo.Session, idleChannelID string, cmdShortcuts []string) GuildPlayer {
	idle := func() {
		if discordvoice.ValidVoiceChannel(discord, idleChannelID) {
			discord.ChannelVoiceJoin(guildID, idleChannelID, false, true)
		}
	}
	return &guildPlayer{
		guildID: guildID,
		discord: discord,
		Player: player.New(
			discordvoice.NewWriterOpener(discord, guildID, 300*time.Millisecond),
			player.QueueLength(10),
			player.IdleFunc(idle, 1000),
		),
		cmdShortcuts: cmdShortcuts,
	}
}

func (gp *guildPlayer) Put(evt GuildEvent, voiceChannelID string, md plugins.Metadata, loudness float64) error {
	statusChannelID, statusMessageID := evt.Channel.ID, ""
	embed := &discordgo.MessageEmbed{
		Color:  0xa680ee,
		Footer: &discordgo.MessageEmbedFooter{},
	}

	refreshStatus := func(playing bool, elapsed time.Duration, lst []string) {
		playPaused := "▶️ "
		if !playing {
			playPaused = "⏸️ "
		}
		embed.Title = playPaused + md.Title
		embed.Description = prettyTime(elapsed) + "/" + prettyTime(md.Duration)

		embed.Fields = nil
		if len(lst) > 0 {
			embed.Fields = []*discordgo.MessageEmbedField{
				&discordgo.MessageEmbedField{
					Name:  "Playlist",
					Value: playlist(lst),
				},
			}
		}

		if statusMessageID == "" {
			msg, err := gp.discord.ChannelMessageSendEmbed(statusChannelID, embed)
			if err != nil {
				log.Printf("failed to display player status %v", err)
				return
			}
			statusMessageID = msg.ID

			gp.mu.Lock()
			gp.nowPlaying = Play{
				statusMessageChannelID: msg.ChannelID,
				statusMessageID:        msg.ID,
				metadata:               md,
			}
			gp.mu.Unlock()

			for _, emoji := range gp.cmdShortcuts {
				if err := gp.discord.MessageReactionAdd(statusChannelID, statusMessageID, emoji); err != nil {
					log.Printf("failed to attach cmd shortcut to player status %v", err)
				}
			}
		} else if _, err := gp.discord.ChannelMessageEditEmbed(statusChannelID, statusMessageID, embed); err != nil {
			log.Printf("failed to refresh player status %v", err)
		}
	}

	err := gp.Enqueue(
		voiceChannelID,
		md.Title,
		md.OpenFunc,
		player.Duration(md.Duration),
		player.Loudness(loudness),
		player.OnStart(func() { refreshStatus(true, 0, gp.Playlist()) }),
		player.OnPause(func(d time.Duration) { refreshStatus(false, d, gp.Playlist()) }),
		player.OnResume(func(d time.Duration) { refreshStatus(true, d, gp.Playlist()) }),
		player.OnProgress(
			func(d time.Duration, frames []time.Time) {
				avg, dev, max, min := statistics(latencies(frames))
				embed.Footer.Text = fmt.Sprintf("avg %.3fms, dev %.3fms, max %.3fms, min %.3fms", avg, dev, max, min)
				refreshStatus(true, d, gp.Playlist())
			},
			5*time.Second,
		),
		player.OnEnd(func(d time.Duration, err error) {
			log.Printf("read %v of %v, expected %v", d, md.Title, md.Duration)
			log.Printf("reason: %v", err)
			if statusMessageID != "" {
				gp.discord.ChannelMessageDelete(statusChannelID, statusMessageID)
				gp.mu.Lock()
				gp.nowPlaying = Play{}
				gp.mu.Unlock()
			}
		}),
	)
	if err == discordvoice.ErrInvalidVoiceChannel {
		return ErrInvalidMusicChannel
	}
	return err
}

func (gp *guildPlayer) NowPlaying() (play Play, ok bool) {
	gp.mu.Lock()
	defer gp.mu.Unlock()
	if gp.nowPlaying.statusMessageID == "" {
		return Play{}, false
	}
	return gp.nowPlaying, true
}

func playlist(lst []string) string {
	buf := &bytes.Buffer{}
	for _, item := range lst {
		buf.WriteString(item + "\n")
	}
	return buf.String()
}
