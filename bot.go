package music

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"
	dgv "github.com/jeffreymkabot/discordvoice"
	"github.com/jeffreymkabot/musicbot/plugins"
)

const defaultPrefix = "#!"
const musicChannelPrefix = "music"

type guild struct {
	guildID string
	play    *dgv.Player
	wg      sync.WaitGroup
	// mutex protects guildInfo fields
	mu sync.RWMutex
	guildInfo
}

func (gu *guild) close() {
	if gu.play != nil {
		gu.play.Quit()
	}
	// wait for status messages to be deleted
	gu.wg.Wait()
}

type guildInfo struct {
	Prefix         string   `json:"prefix"`
	ListenChannels []string `json:"listen"`
}

// BotOptions configure runtime parameters of the bot
type BotOption func(*Bot)

// Soundcloud sets the clientID required by the soundcloud API and enables use of the soundcloud command.
func Soundcloud(clientID string) BotOption {
	return func(b *Bot) {
		if clientID != "" {
			b.soundcloud = clientID
			b.commands = append(b.commands, soundcloud)
		}
	}
}

// Loudness sets the loudness target.  Higher is louder.
// See https://ffmpeg.org/ffmpeg-filters.html#loudnorm.
// Values less than -70.0 or greater than -5.0 have no effect.
// In particular, the default value of 0 has no effect and input streams will be unchanged.
func Loudness(f float64) BotOption {
	return func(b *Bot) {
		if -70 <= f && f <= -5 {
			b.loudness = f
		}
	}
}

type Bot struct {
	mu         sync.RWMutex
	session    *discordgo.Session
	db         *bolt.DB
	owner      string
	soundcloud string
	loudness   float64
	guilds     map[string]*guild
	commands   []*command
}

func New(token string, dbPath string, owner string, opts ...BotOption) (*Bot, error) {
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		return nil, err
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("guilds"))
		return err
	}); err != nil {
		return nil, err
	}

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	session.LogLevel = discordgo.LogWarning
	b := &Bot{
		session: session,
		db:      db,
		owner:   owner,
		guilds:  make(map[string]*guild),
		commands: []*command{
			help,
			youtube,
			skip,
			pause,
			clear,
			reconnect,
			// setPrefix,
			setListen,
			unsetListen,
		},
	}

	for _, opt := range opts {
		opt(b)
	}

	log.Printf("available commands %#v", b.commands)

	session.AddHandler(onGuildCreate(b))
	session.AddHandler(onMessageCreate(b))
	session.AddHandler(onReady(b))

	err = session.Open()
	if err != nil {
		db.Close()
		return nil, err
	}

	return b, nil
}

func (b *Bot) Stop() {
	b.mu.Lock()
	for _, gu := range b.guilds {
		gu.close()
	}
	b.session.Close()
	b.db.Close()
	b.mu.Unlock()
}

func (b *Bot) Enqueue(guildID string, plugin plugins.Plugin, url string, statusChannelID string) error {
	b.mu.RLock()
	gu, ok := b.guilds[guildID]
	b.mu.RUnlock()
	if !ok || gu.play == nil {
		return errors.New("no player for guild id " + guildID)
	}

	return b.enqueue(gu, plugin, url, statusChannelID)
}

func (b *Bot) enqueue(gu *guild, plugin plugins.Plugin, url string, statusChannelID string) error {
	musicChannelID := musicChannelFromGuildID(b.session.State, gu.guildID)
	if musicChannelID == "" {
		return errors.New("no music channel set up")
	}

	md, err := plugin.DownloadURL(url)
	if err != nil {
		return err
	}

	var msg *discordgo.Message
	embed := &discordgo.MessageEmbed{}
	embed.Color = 0xa680ee
	update := func() {
		if msg == nil {
			msg, err = b.session.ChannelMessageSendEmbed(statusChannelID, embed)
			if msg != nil {
				// gu.close() will now wait until the message is deleted
				gu.wg.Add(1)
			}
		} else {
			b.session.ChannelMessageEditEmbed(msg.ChannelID, msg.ID, embed)
		}
	}

	return gu.play.Enqueue(
		musicChannelID,
		md.DownloadURL,
		dgv.Title(md.Title),
		dgv.Duration(md.Duration),
		dgv.Loudness(b.loudness),
		dgv.OnStart(func() {
			embed.Title = "▶️ " + md.Title
			embed.Description = prettyTime(0) + "/" + prettyTime(md.Duration)
			update()
		}),
		dgv.OnPause(func(elapsed time.Duration) {
			embed.Title = "⏸️ " + md.Title
			embed.Description = prettyTime(elapsed) + "/" + prettyTime(md.Duration)
			update()
		}),
		dgv.OnResume(func(elapsed time.Duration) {
			embed.Title = "▶️ " + md.Title
			embed.Description = prettyTime(elapsed) + "/" + prettyTime(md.Duration)
			update()
		}),
		dgv.OnProgress(func(elapsed time.Duration) {
			embed.Title = "▶️ " + md.Title
			embed.Description = prettyTime(elapsed) + "/" + prettyTime(md.Duration)
			update()
		}, 5*time.Second),
		dgv.OnEnd(func(elapsed time.Duration, err error) {
			log.Printf("read %v of %v, expected %v", elapsed, md.Title, md.Duration)
			log.Printf("reason: %v", err)
			if msg != nil {
				b.session.ChannelMessageDelete(msg.ChannelID, msg.ID)
				gu.wg.Done()
			}
		}),
	)
}

func (b *Bot) addGuild(g *discordgo.Guild) {
	// cleanup existing guild structure if exists
	b.mu.RLock()
	gu, ok := b.guilds[g.ID]
	b.mu.RUnlock()
	if ok {
		gu.close()
		// sometimes reconnecting does not produce a viable voice connection
		// waiting a few seconds seems to help
		// TODO figure out why and handle this in better way
		time.Sleep(3 * time.Second)
	}

	guInfo := guildInfo{}
	if err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("guilds"))
		val := bucket.Get([]byte(g.ID))
		if val == nil {
			return nil
		}
		return json.Unmarshal(val, &guInfo)
	}); err != nil {
		log.Printf("error lookup guild in db %v", err)
	} else {
		log.Printf("using guildinfo %#v", guInfo)
	}

	// prefer to idle in the music channel if one exists at this time
	musicChannel := musicChannelFromGuild(g)
	if musicChannel == "" {
		musicChannel = g.AfkChannelID
	}
	player := dgv.Connect(b.session, g.ID, musicChannel, dgv.QueueLength(10))
	b.mu.Lock()
	b.guilds[g.ID] = &guild{
		guildID:   g.ID,
		play:      player,
		guildInfo: guInfo,
	}
	b.mu.Unlock()
}

func (b *Bot) exec(cmd *command, env *environment, gu *guild, args []string) {
	if gu == nil {
		return
	}

	gu.mu.RLock()
	if cmd.restrictChannel && !contains(gu.ListenChannels, env.channel.ID) {
		gu.mu.RUnlock()
		log.Printf("command %s invoked in unregistered channel %s", cmd.name, env.channel.ID)
		return
	}
	gu.mu.RUnlock()

	if cmd.ownerOnly && b.owner != env.message.Author.ID {
		log.Printf("user %s not allowed to execute command %s", env.message.Author.ID, cmd.name)
		return
	}

	log.Printf("exec command %v in %v with %v", cmd.name, gu.guildID, args)
	err := cmd.run(b, env, gu, args)
	if err != nil {
		b.session.ChannelMessageSend(env.channel.ID, fmt.Sprintf("🤔...\n%v", err))
	} else if cmd.ack != "" {
		b.session.MessageReactionAdd(env.channel.ID, env.message.ID, cmd.ack)
	}
}

func musicChannelFromGuildID(state *discordgo.State, guildID string) string {
	g, err := state.Guild(guildID)
	if err != nil {
		return ""
	}
	return musicChannelFromGuild(g)
}

func musicChannelFromGuild(g *discordgo.Guild) string {
	for _, ch := range g.Channels {
		if ch.Type == discordgo.ChannelTypeGuildVoice && strings.HasPrefix(strings.ToLower(ch.Name), musicChannelPrefix) {
			return ch.ID
		}
	}
	return ""
}

func contains(s []string, t string) bool {
	for _, v := range s {
		if v == t {
			return true
		}
	}
	return false
}

func prettyTime(t time.Duration) string {
	hours := int(t.Hours())
	min := int(t.Minutes()) % 60
	sec := int(t.Seconds()) % 60
	if hours >= 1 {
		return fmt.Sprintf("%02v:%02v:%02v", hours, min, sec)
	}
	return fmt.Sprintf("%02v:%02v", min, sec)
}
