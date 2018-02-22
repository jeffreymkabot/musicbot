package music

import (
	"errors"
	"log"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	dcv "github.com/jeffreymkabot/discordvoice"
	"github.com/jeffreymkabot/musicbot/plugins"
)

type guildClient interface {
	send(guildRequest)
	close()
}

type syncGuildClient struct {
	ch chan<- guildRequest
	wg sync.WaitGroup
}

func (gh syncGuildClient) send(req guildRequest) {
	gh.ch <- req
}

func (gh *syncGuildClient) close() {
	if gh.ch != nil {
		close(gh.ch)
		gh.ch = nil
		gh.wg.Wait()
	}
}

type guildService struct {
	guildInfo
	guildID   string
	store     guildStorage
	session   *discordgo.Session
	statusMsg *discordgo.Message
	player    *dcv.Player
}

type guildStorage interface {
	read(guildID string, info *guildInfo) error
	write(guildID string, info guildInfo) error
}

type guildInfo struct {
	Prefix         string   `json:"prefix"`
	ListenChannels []string `json:"listen"`
	MusicChannel   string   `json:"play"`
	// Loudness sets the loudness target.  Higher is louder.
	// See https://ffmpeg.org/ffmpeg-filters.html#loudnorm.
	// Values less than -70.0 or greater than -5.0 have no effect.
	// In particular, the default value of 0 has no effect and audio streams will be unchanged.
	Loudness float64 `json:"loudness"`
}

var defaultGuildInfo = guildInfo{
	Prefix: defaultCommandPrefix,
}

type guildRequest struct {
	guildID  string
	message  *discordgo.Message
	channel  *discordgo.Channel
	command  command
	callback func(err error)
}

func newGuild(session *discordgo.Session, guild *discordgo.Guild, store guildStorage) guildClient {
	gsvc := guildService{
		guildID: guild.ID,
		store:   store,
		session: session,
	}

	if err := store.read(guild.ID, &gsvc.guildInfo); err != nil {
		gsvc.guildInfo = defaultGuildInfo
		gsvc.MusicChannel = detectMusicChannel(guild)
	}

	idleChannel := guild.AfkChannelID
	if gsvc.MusicChannel != "" {
		idleChannel = gsvc.MusicChannel
	}

	gsvc.player = dcv.Connect(
		session,
		guild.ID,
		idleChannel,
		dcv.QueueLength(10),
	)

	ch := make(chan guildRequest)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		for request := range ch {
			gsvc.handle(request)
		}
		// chan closed
		gsvc.player.Quit()
		wg.Done()
	}()
	return &syncGuildClient{ch, wg}
}

func (gsvc *guildService) handle(req guildRequest) {
	if !strings.HasPrefix(req.message.Content, gsvc.Prefix) && !strings.HasPrefix(req.message.Content, defaultCommandPrefix) {
		return
	}

	args := strings.Fields(strings.TrimPrefix(req.message.Content, gsvc.Prefix))
	cmd, args := parseCommand(args)
	if cmd == nil {
		return
	}

	if cmd.restrictChannel && !contains(gsvc.ListenChannels, req.channel.ID) {
		log.Printf("command %s invoked in unregistered channel %s", cmd.name, req.channel.ID)
		return
	}

	if cmd.ownerOnly && !isOwner(req.message.Author.ID) {
		log.Printf("user %s not allowed to execute command %s", req.message.Author.ID, cmd.name)
		return
	}

	// TODO
	err := cmd.run(gsvc, req, args)
	if err == nil && cmd.ack != "" {
		gsvc.session.MessageReactionAdd(req.channel.ID, req.message.ID, cmd.ack)
	}
	if req.callback != nil {
		req.callback(err)
	}
}

func (gsvc *guildService) enqueue(p plugins.Plugin, arg string, statusChannelID string) error {
	musicChannelID := gsvc.MusicChannel
	if musicChannelID == "" {
		return errors.New("no music channel set up")
	}

	md, err := p.Resolve(arg)
	if err != nil {
		return err
	}

	return gsvc.player.Enqueue(
		musicChannelID,
		md.Title,
		md.Open,
		dcv.Duration(md.Duration),
		dcv.Loudness(gsvc.Loudness),
		// TODO status message
	)
}

func (gsvc *guildService) save() error {
	return gsvc.store.write(gsvc.guildID, gsvc.guildInfo)
}

func (gsvc *guildService) reconnect() {
	gsvc.player.Quit()
	// TODO
}

func contains(s []string, t string) bool {
	for _, v := range s {
		if v == t {
			return true
		}
	}
	return false
}

// TODO
func isOwner(userID string) bool {
	return false
}
