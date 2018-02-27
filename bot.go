package music

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"
	"github.com/jeffreymkabot/musicbot/plugins"
)

const defaultCommandPrefix = "#!"
const defaultMusicChannelPrefix = "music"

type Bot struct {
	owner         string
	me            *discordgo.User
	discord       *discordgo.Session
	db            *boltGuildStorage
	commands      []command
	plugins       []plugins.Plugin
	mu            sync.RWMutex
	guildServices map[string]GuildService
}

func New(token string, dbPath string, owner string, soundcloud string) (*Bot, error) {
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
		owner:   owner,
		discord: discord,
		db:      db,
		commands: []command{
			help,
			pause,
			skip,
			clear,
			reconnect,
			get,
			set,
			setListen,
			unsetListen,
		},
		plugins: []plugins.Plugin{
			plugins.Youtube{},
			plugins.Soundcloud{ClientID: soundcloud},
			plugins.Twitch{},
			plugins.Bandcamp{},
			plugins.Streamlink{},
		},
		guildServices: make(map[string]GuildService),
	}

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

func (b *Bot) Stop() {
	b.mu.Lock()
	for _, svc := range b.guildServices {
		svc.Close()
	}
	b.discord.Close()
	b.db.Close()
	b.mu.Unlock()
}

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
	playerOpener := func(idleChannelID string) GuildPlayer {
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
		playerOpener,
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
