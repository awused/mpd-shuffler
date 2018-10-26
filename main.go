package main

import (
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/awused/awconf"
	"github.com/awused/go-strpick/persistent"
	"github.com/fhs/gompd/mpd"
)

type config struct {
	MPDNetwork  string
	MPDAddress  string
	MPDPassword string
	DatabaseDir string
	MPDTimeout  int
	PathPrefix  string
	KeepLast    int
	Debug       bool
}

var closeChan = make(chan struct{})
var conf *config
var wg sync.WaitGroup

func main() {
	err := awconf.LoadConfig("mpd-shuffler", &conf)
	if err != nil {
		log.Fatal(err)
	}

	watch, err := mpd.NewWatcher(
		conf.MPDNetwork, conf.MPDAddress, conf.MPDPassword,
		"database", "player", "playlist")
	checkErr(err)
	defer watch.Close()

	client, err := mpd.DialAuthenticated(
		conf.MPDNetwork, conf.MPDAddress, conf.MPDPassword)
	checkErr(err)
	defer client.Close()

	picker, err := persistent.NewPicker(conf.DatabaseDir)
	checkErr(err)
	defer picker.Close()

	pingLoop(client)

	handleDatabaseChange(client, picker)
	handlePlaylistChange(client)
	handlePlayerChange(client, picker)
	watchLoop(watch, client, picker)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	<-sigs
	signal.Reset(syscall.SIGINT, syscall.SIGTERM)

	log.Println("SIGINT/SIGTERM caught, exiting")
	close(closeChan)
	wg.Wait()
	os.Exit(0)
}

func pingLoop(client *mpd.Client) {
	wg.Add(1)

	go func() {
		defer wg.Done()
		for true {
			select {
			case <-time.After(time.Duration(conf.MPDTimeout) * time.Second):
				if conf.Debug {
					log.Println("ping")
				}
				err := client.Ping()
				checkErr(err)
			case <-closeChan:
				return
			}
		}
	}()
}

func checkErr(err error) {
	if err != nil {
		log.Println(err)
		panic(err)
	}
}
