package main

import (
	"log"
	"os"
	"os/signal"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/awused/awconf"
	"github.com/awused/go-strpick/persistent"
	"github.com/fhs/gompd/mpd"
)

type config struct {
	MPDNetwork    string
	MPDAddress    string
	MPDPassword   string
	DatabaseDir   string
	MPDTimeout    int
	PathRegex     string
	KeepLast      int
	DisableRepeat bool
	Debug         bool
}

var closeChan chan struct{}
var errorChan = make(chan error)
var conf *config
var pathRegex *regexp.Regexp
var wg sync.WaitGroup

func main() {
	err := awconf.LoadConfig("mpd-shuffler", &conf)
	if err != nil {
		log.Fatal(err)
	}

	pathRegex = regexp.MustCompile(conf.PathRegex)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	for true {
		closeChan = make(chan struct{})

		go run()
		// TODO -- Handle the unlikely race condition where both goroutines spawned
		// by run() fail before this routine is ready to read from errorChan
		select {
		case <-sigs:
			signal.Reset(syscall.SIGINT, syscall.SIGTERM)
			log.Println("SIGINT/SIGTERM caught, exiting")
			close(closeChan)
			wg.Wait()
			os.Exit(0)
		case err = <-errorChan:
			log.Printf("Error: %s", err)
			close(closeChan)
			wg.Wait()
		}

		log.Println("Reconnecting in one minute")

		// Will have to refactor if there's a reason to allow reloading configs
		select {
		case <-sigs:
			signal.Reset(syscall.SIGINT, syscall.SIGTERM)
			log.Println("SIGINT/SIGTERM caught, exiting")
			os.Exit(0)
		case <-time.After(60 * time.Second):
		}
	}
}

// TODO -- refactor this more
type shuffler struct {
	state          string
	lastStateAttrs map[string]string
	lastFiles      map[string]bool
	playlist       map[string]int
}

var s shuffler

func run() {
	s = shuffler{}

	watch, err := mpd.NewWatcher(
		conf.MPDNetwork, conf.MPDAddress, conf.MPDPassword,
		"database", "player", "playlist", "options")
	if err != nil {
		sendError(err)
		return
	}
	defer watch.Close()

	client, err := mpd.DialAuthenticated(
		conf.MPDNetwork, conf.MPDAddress, conf.MPDPassword)
	if err != nil {
		sendError(err)
		return
	}
	defer client.Close()

	picker, err := persistent.NewPicker(conf.DatabaseDir)
	if err != nil {
		sendError(err)
		return
	}
	defer picker.Close()

	err = handleDatabaseChange(client, picker)
	if err != nil {
		sendError(err)
		return
	}
	// Correct options before initializing the playlist
	// Disabling repeat mode will not trigger a playlist event
	err = handleOptionsChange(client)
	if err != nil {
		sendError(err)
		return
	}
	err = handlePlaylistChange(client, picker)
	if err != nil {
		sendError(err)
		return
	}
	err = handlePlayerChange(client, picker)
	if err != nil {
		sendError(err)
		return
	}
	pingLoop(client)
	watchLoop(watch, client, picker)

	wg.Wait()
	return
}

// Blocks to send the error on errorChan unless this shuffler is closed
func sendError(err error) {
	select {
	case errorChan <- err:
	case <-closeChan:
	}
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
				if err != nil {
					sendError(err)
					return
				}
			case <-closeChan:
				return
			}
		}
	}()
}
