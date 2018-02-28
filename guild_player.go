package music

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	dcv "github.com/jeffreymkabot/discordvoice"
	"github.com/jeffreymkabot/musicbot/plugins"
)

var ErrInvalidMusicChannel = errors.New("Set a valid voice channel for music playback, then call reconnect.")

type GuildPlayer interface {
	Enqueue(evt GuildEvent, voiceChannelID string, md *plugins.Metadata, loudness float64) error
	Skip()
	Pause()
	Clear()
	Close()
	NowPlaying() (Play, bool)
}

type Play struct {
	statusMessage *discordgo.Message
	metadata      *plugins.Metadata
}

type guildPlayer struct {
	guildID      string
	discord      *discordgo.Session
	player       *dcv.Player
	cmdShortcuts []string
	wg           sync.WaitGroup
	mu           sync.Mutex
	// TODO how to manage statusMessage state in a reasonable way without mutex?
	nowPlaying Play
}

func NewGuildPlayer(guildID string, discord *discordgo.Session, idleChannelID string, cmdShortcuts []string) GuildPlayer {
	return &guildPlayer{
		guildID: guildID,
		discord: discord,
		player: dcv.Connect(
			discord,
			guildID,
			idleChannelID,
			dcv.QueueLength(10),
		),
		cmdShortcuts: cmdShortcuts,
	}
}

// TODO don't use nullable metadata
func (gp *guildPlayer) Enqueue(evt GuildEvent, voiceChannelID string, md *plugins.Metadata, loudness float64) error {
	statusChannelID, statusMessageID := evt.Channel.ID, ""
	embed := &discordgo.MessageEmbed{}
	embed.Color = 0xa680ee
	embed.Footer = &discordgo.MessageEmbedFooter{}
	refreshStatus := func(playing bool, elapsed time.Duration, next string) {
		embed.Title = "▶️ " + md.Title
		if !playing {
			embed.Title = "⏸️ " + md.Title
		}
		embed.Description = prettyTime(elapsed) + "/" + prettyTime(md.Duration)
		embed.Footer.Text = ""
		if next != "" {
			embed.Footer.Text = "On Deck: " + next
		}

		if statusMessageID == "" {
			msg, err := gp.discord.ChannelMessageSendEmbed(statusChannelID, embed)
			if err != nil {
				log.Printf("failed to display player status %v", err)
				return
			}
			statusMessageID = msg.ID

			// wait for the status message to be deleted when the guild player closes
			gp.wg.Add(1)
			gp.mu.Lock()
			gp.nowPlaying = Play{
				statusMessage: msg,
				metadata:      md,
			}
			gp.mu.Unlock()

			for _, emoji := range gp.cmdShortcuts {
				gp.discord.MessageReactionAdd(statusChannelID, statusMessageID, emoji)
			}
		} else {
			_, err := gp.discord.ChannelMessageEditEmbed(statusChannelID, statusMessageID, embed)
			if err != nil {
				log.Printf("failed to refresh player status %v", err)
			}
		}
	}

	err := gp.player.Enqueue(
		voiceChannelID,
		md.Title,
		md.Open,
		dcv.Duration(md.Duration),
		dcv.Loudness(loudness),
		dcv.OnStart(func() { refreshStatus(true, 0, gp.player.Next()) }),
		dcv.OnPause(func(d time.Duration) { refreshStatus(false, d, gp.player.Next()) }),
		dcv.OnResume(func(d time.Duration) { refreshStatus(true, d, gp.player.Next()) }),
		dcv.OnProgress(
			func(d time.Duration, frames []time.Time) {
				avg, dev, max, min := statistics(latencies(frames))
				embed.Fields = []*discordgo.MessageEmbedField{
					&discordgo.MessageEmbedField{
						Name:  "Debug",
						Value: fmt.Sprintf("`avg %.3fms`, `dev %.3fms`, `max %.3fms`, `min %.3fms`", avg, dev, max, min),
					},
				}
				refreshStatus(true, d, gp.player.Next())
			},
			5*time.Second,
		),
		dcv.OnEnd(func(d time.Duration, err error) {
			log.Printf("read %v of %v, expected %v", d, md.Title, md.Duration)
			log.Printf("reason: %v", err)
			if statusMessageID != "" {
				gp.discord.ChannelMessageDelete(statusChannelID, statusMessageID)
				gp.mu.Lock()
				gp.nowPlaying = Play{}
				gp.mu.Unlock()
				gp.wg.Done()
			}
		}),
	)
	if err == dcv.ErrInvalidVoiceChannel {
		return ErrInvalidMusicChannel
	}
	return err
}

func (gp *guildPlayer) Skip() {
	gp.player.Skip()
}

func (gp *guildPlayer) Pause() {
	gp.player.Pause()
}

func (gp *guildPlayer) Clear() {
	gp.player.Clear()
}

func (gp *guildPlayer) Close() {
	gp.player.Quit()
	gp.wg.Wait()
}

func (gp *guildPlayer) NowPlaying() (Play, bool) {
	gp.mu.Lock()
	defer gp.mu.Unlock()
	if gp.nowPlaying.metadata == nil || gp.nowPlaying.statusMessage == nil {
		return Play{}, false
	}
	return gp.nowPlaying, true
}

func xvalidVoiceChannel(discord *discordgo.Session, channelID string) bool {
	channel, err := discord.State.Channel(channelID)
	if err != nil {
		channel, err = discord.Channel(channelID)
	}
	return err == nil && channel.Type == discordgo.ChannelTypeGuildVoice
}
