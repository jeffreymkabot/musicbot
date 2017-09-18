package music

import (
	"errors"
	"log"
	"sync"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"
	dgv "github.com/jeffreymkabot/aoebot/discordvoice"
)

const (
	Ready = iota
	Playing
	Paused
)

const defaultPrefix = "##"

type guild struct {
	mu      sync.Mutex
	guildID string
	state   int
	queue   chan<- *dgv.Payload
	quit    func()
	guildInfo
}

type guildInfo struct {
	Prefix string `json:"prefix"`
	// PlayChannel    string   `json:"play"`
	ListenChannels []string `json:"listen"`
}

type Bot struct {
	mu       sync.RWMutex
	session  *discordgo.Session
	db       *bolt.DB
	owner    string
	guilds   map[string]*guild
	commands []*command
}

func New(token string, dbPath string, owner string) (*Bot, error) {
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
	b := &Bot{
		session: session,
		db:      db,
		owner:   owner,
		guilds:  make(map[string]*guild),
		commands: []*command{
			help,
			youtube,
			pause,
			unpause,
			stop,
			setPrefix,
			setListen,
			unsetListen,
		},
	}

	session.AddHandlerOnce(onReady(b))

	err = session.Open()
	if err != nil {
		db.Close()
		return nil, err
	}

	return b, nil
}

func (b *Bot) Stop() {
	b.mu.Lock()
	b.session.Close()
	b.db.Close()
	b.mu.Unlock()
}

func (b *Bot) exec(cmd *command, g *guild, authorID string, textChannelID string, args []string) error {
	goodChannel := !cmd.isListenChannelOnly
	if !goodChannel {
		for _, ch := range g.ListenChannels {
			if ch == textChannelID {
				goodChannel = true
			}
		}
	}
	if !goodChannel {
		return errors.New("unregistered command channel")
	}

	if cmd.isOwnerOnly && b.owner != authorID {
		return errors.New("user not allowed to execute this command")
	}

	log.Printf("exec command %v in %v with %v\n", cmd.name, g.guildID, args)
	return cmd.run(b, g, textChannelID, args)
}
