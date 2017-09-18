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
		Token string
		Bolt  string
		Owner string
	}
	_, err := toml.DecodeFile(*cfgFile, &cfg)
	if err != nil {
		log.Fatalf("Error opening cfg file: %v", err)
	}

	bot, err := music.New(cfg.Token, cfg.Bolt, cfg.Owner)
	defer bot.Stop()
	if err != nil {
		log.Fatalf("%v", err)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	sig := <-c
	switch sig {
	case os.Interrupt:
		return
	case os.Kill:
		os.Exit(1)
	}
}
