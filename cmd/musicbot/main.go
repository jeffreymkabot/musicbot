package main

import (
	"flag"
	"log"
	"os"
	"os/signal"

	"github.com/BurntSushi/toml"
	"github.com/jeffreymkabot/musicbot"
)

func main() {
	cfgFile := flag.String("cfg", "config.toml", "Config File Path")
	flag.Parse()
	if *cfgFile == "" {
		flag.Usage()
		os.Exit(1)
	}

	var cfg struct {
		Token      string
		Bolt       string
		Soundcloud string
		Youtube    string
	}
	_, err := toml.DecodeFile(*cfgFile, &cfg)
	if err != nil {
		log.Fatalf("Failed to open cfg file: %v", err)
	}

	bot, err := musicbot.New(cfg.Token, cfg.Bolt, cfg.Soundcloud, cfg.Youtube)
	if err != nil {
		log.Fatalf("Failed to start bot: %v", err)
	}
	defer bot.Stop()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	sig := <-c
	switch sig {
	case os.Interrupt:
		log.Print("SIGINT")
		return
	case os.Kill:
		os.Exit(1)
	}
}
