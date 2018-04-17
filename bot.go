package music

import (
	"encoding/json"
	"errors"
	"log"
	"sync"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"
	"github.com/jeffreymkabot/musicbot/plugins"
)

const defaultCommandPrefix = "#!"
const defaultMusicChannelPrefix = "music"

// Bot provides resources to and routes events to the appropriate guild service.
type Bot struct {
	me            *discordgo.User
	discord       *discordgo.Session
	db            *boltGuildStorage
	commands      []command
	plugins       []plugins.Plugin
	mu            sync.RWMutex
	guildServices map[string]GuildService
}

// New starts a musicbot server.
func New(token string, dbPath string, soundcloud string, youtube string) (*Bot, error) {
	db, err := newBoltGuildStorage(dbPath)
	if err != nil {
		return nil, err
	}

	discord, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	discord.LogLevel = discordgo.LogWarning

	b := &Bot{
		discord: discord,
		db:      db,
		commands: []command{
			help,
			playlist,
			pause,
			skip,
			clear,
			requeue,
			reconnect,
			get,
			set,
			setPlayback,
			setListen,
			unsetListen,
		},
		plugins: []plugins.Plugin{
			plugins.Youtube{},
			plugins.Soundcloud{ClientID: soundcloud},
			plugins.Twitch{},
			plugins.Bandcamp{},
		},
		guildServices: make(map[string]GuildService),
	}

	youtubeSearch, err := plugins.NewYoutubeSearch(youtube)
	if err == nil {
		b.plugins = append(b.plugins, youtubeSearch)
	} else {
		log.Printf("failed to acquire youtube service %v", err)
	}
	b.plugins = append(b.plugins, plugins.Streamlink{})

	discord.AddHandler(onGuildCreate(b))
	discord.AddHandler(onMessageCreate(b))
	discord.AddHandler(onMessageReactionAdd(b))
	discord.AddHandler(onMessageReactionRemove(b))
	discord.AddHandler(onReady(b))

	err = discord.Open()
	if err != nil {
		db.Close()
		return nil, err
	}

	// possible to take this from ready instead???
	b.me, err = discord.User("@me")
	if err != nil {
		discord.Close()
		db.Close()
		return nil, err
	}

	return b, nil
}

// Stop closes all services and resources.
func (b *Bot) Stop() {
	b.mu.Lock()
	for _, svc := range b.guildServices {
		svc.Close()
	}
	b.discord.Close()
	b.db.Close()
	b.mu.Unlock()
}

// AddGuild registers a new guild service to which discord events may be routed.
func (b *Bot) AddGuild(guild *discordgo.Guild) {
	// cleanup existing guild service if exists
	// e.g. unhandled disconnect, kick and reinvite
	b.mu.Lock()
	defer b.mu.Unlock()
	if svc, ok := b.guildServices[guild.ID]; ok {
		svc.Close()
	}

	// alternative is to lookup guild in database here and resolve idlechannel immediately,
	// would have to lookup guild twice or pass info into guild fn
	openPlayer := func(idleChannelID string) GuildPlayer {
		return NewGuildPlayer(
			guild.ID,
			b.discord,
			idleChannelID,
			commandShortcuts(b.commands),
		)
	}
	b.guildServices[guild.ID] = Guild(
		guild,
		b.discord,
		b.db,
		openPlayer,
		b.commands,
		b.plugins,
	)
}

type boltGuildStorage struct {
	*bolt.DB
}

func newBoltGuildStorage(dbPath string) (*boltGuildStorage, error) {
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
	return &boltGuildStorage{db}, nil
}

func (db boltGuildStorage) Get(guildID string) (GuildInfo, error) {
	info := GuildInfo{}
	err := db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("guilds"))
		val := bucket.Get([]byte(guildID))
		if val == nil {
			return errors.New("guild not found")
		}
		return json.Unmarshal(val, &info)
	})
	return info, err
}

func (db boltGuildStorage) Put(guildID string, info GuildInfo) error {
	val, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("guilds"))
		return bucket.Put([]byte(guildID), val)
	})
}
