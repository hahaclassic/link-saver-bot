package main

import (
	"context"
	"flag"
	"log"
	"os"

	tgClient "github.com/hahaclassic/golang-telegram-bot.git/clients/telegram"
	event_consumer "github.com/hahaclassic/golang-telegram-bot.git/consumer/event-consumer"
	"github.com/hahaclassic/golang-telegram-bot.git/events/telegram"
	"github.com/hahaclassic/golang-telegram-bot.git/storage/sqlite"
)

const (
	tgBotHost         = "api.telegram.org"
	sqliteStoragePath = "data/sqlite/data.db"
	batchSize         = 100
)

func main() {
	if _, err := os.Stat("/path/to/whatever"); os.IsNotExist(err) {
		err := os.MkdirAll("./data/sqlite/", 0777)

		if err != nil {
			log.Fatal("can't create directory")
		}
	}

	// Create database
	s, err := sqlite.New(sqliteStoragePath)
	if err != nil {
		log.Fatalf("can't connect to storage: %s", err)
	}

	err = s.Init(context.TODO())
	if err != nil {
		log.Fatalf("can't init storage: %s", err)
	}

	// Create events Processor
	eventsProcessor := telegram.New(tgClient.New(tgBotHost, mustToken()), s)

	log.Print("[START]")

	// Create consumer
	consumer := event_consumer.New(eventsProcessor, eventsProcessor, batchSize)
	if err := consumer.Start(); err != nil {
		log.Fatal("service is stopped", err)
	}
}

func mustToken() string {
	token := flag.String(
		"tg-bot-token",
		"",
		"token for access to telegram bot",
	)

	flag.Parse()

	if *token == "" {
		log.Fatal("token is not specified")
	}

	return *token
}
