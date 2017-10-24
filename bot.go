package music

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"
	dgv "github.com/jeffreymkabot/discordvoice"
	"github.com/jeffreymkabot/musicbot/plugins"
)

const defaultPrefix = "#!"

type guild struct {
	guildID string
	play    *dgv.Player
	wg      sync.WaitGroup
	// mutex protects guildInfo fields
	mu sync.RWMutex
	guildInfo
}

func (g *guild) close() {
	if g.play != nil {
		g.play.Quit()
	}
	g.wg.Wait()
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
			log.Printf("activate soundcloud")
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
			// help,
			youtube,
			skip,
			pause,
			clear,
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
	for _, g := range b.guilds {
		g.close()
	}
	b.session.Close()
	b.db.Close()
	b.mu.Unlock()
}

func (b *Bot) Enqueue(g *guild, plugin plugins.Plugin, url string, statusChannelID string) error {
	voiceChannelID := guildMusicChannelID(b.session, g.guildID)
	if voiceChannelID == "" {
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
			msg, _ = b.session.ChannelMessageSendEmbed(statusChannelID, embed)
		} else {
			b.session.ChannelMessageEditEmbed(msg.ChannelID, msg.ID, embed)
		}
	}
	
	return g.play.Enqueue(
		voiceChannelID,
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
		}, 5 * time.Second),
		dgv.OnEnd(func() {
			if msg != nil {
				b.session.ChannelMessageDelete(msg.ChannelID, msg.ID)
			}
		}),
	)
}

func (b *Bot) exec(cmd *command, g *guild, authorID string, messageID string, textChannelID string, args []string) {
	g.mu.RLock()
	if cmd.listenChannel && !contains(g.ListenChannels, textChannelID) {
		g.mu.RUnlock()
		log.Printf("command %s invoked in unregistered channel", cmd.name)
		return
	}
	g.mu.RUnlock()

	if cmd.ownerOnly && b.owner != authorID {
		log.Printf("user %s not allowed to execute this command", authorID)
		return
	}

	log.Printf("exec command %v in %v with %v\n", cmd.name, g.guildID, args)
	err := cmd.run(b, g, textChannelID, args)
	if err != nil {
		b.session.ChannelMessageSend(textChannelID, fmt.Sprintf("🤔...\n%v", err))
	} else if cmd.ack != "" {
		b.session.MessageReactionAdd(textChannelID, messageID, cmd.ack)
	}
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
