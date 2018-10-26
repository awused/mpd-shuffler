package main

import (
	"log"
	"strconv"
	"strings"

	strpick "github.com/awused/go-strpick"
	"github.com/fhs/gompd/mpd"
)

// Detect and ignore duplicated player events
var lastStateAttrs map[string]string

var state string
var lastFiles = make(map[string]bool)

var playlist map[string]int

func addNextTrack(
	client *mpd.Client, picker strpick.Picker,
	currentSongIndex int, currentSongID int) bool {
	f, err := picker.Next()
	checkErr(err)

	for currentSongID == playlist[f] {
		if conf.Debug {
			log.Printf("Selected song [%s] but it was already playing\n", f)
		}
		sz, err := picker.Size()
		checkErr(err)
		if sz <= 1 {
			log.Println("No other songs are available to play")
			return false
		}

		f, err = picker.Next()
		checkErr(err)
	}

	if conf.Debug {
		log.Println("Adding song " + f)
	}

	if id, ok := playlist[f]; ok {
		if conf.Debug {
			log.Println("Song is already on the playlist, moving")
		}

		checkErr(client.MoveID(id, currentSongIndex))
		return false
	}

	checkErr(client.Add(f))
	return true
}

func handlePlayerChange(client *mpd.Client, picker strpick.Picker) {
	attrs, err := client.Status()
	checkErr(err)

	if lastStateAttrs["state"] == attrs["state"] &&
		lastStateAttrs["song"] == attrs["song"] &&
		lastStateAttrs["songid"] == attrs["songid"] &&
		lastStateAttrs["nextsongid"] == attrs["nextsongid"] {
		return
	}
	lastStateAttrs = attrs

	state = attrs["state"]
	if conf.Debug {
		log.Println("state: " + state)
	}

	if state != "play" {
		return
	}

	currentSongIndex, err := strconv.Atoi(attrs["song"])
	checkErr(err)
	if conf.Debug {
		log.Printf("Song index: %d\n", currentSongIndex)
	}

	_, hasNextSong := attrs["nextsongid"]
	addedNewTrack := false

	if !hasNextSong {
		if conf.Debug {
			log.Println("Reached end of playlist")
		}

		sz, err := picker.Size()
		checkErr(err)
		if sz == 0 {
			log.Println("No valid files to add to end of playlist")
			return
		}

		currentSongID, err := strconv.Atoi(attrs["songid"])
		checkErr(err)
		addedNewTrack =
			addNextTrack(client, picker, currentSongIndex, currentSongID)
	}

	if conf.KeepLast >= 0 && (hasNextSong || addedNewTrack) {
		if currentSongIndex > conf.KeepLast {
			end := currentSongIndex - conf.KeepLast
			if conf.Debug {
				log.Printf("Removing songs [%d, %d)\n", 0, end)
			}
			err := client.Delete(0, end)
			checkErr(err)
		}
	}

}

func handlePlaylistChange(client *mpd.Client) {
	tracks, err := client.PlaylistInfo(-1, -1)
	checkErr(err)

	playlist = make(map[string]int)
	for _, t := range tracks {
		songid, err := strconv.Atoi(t["Id"])
		checkErr(err)

		playlist[t["file"]] = songid
	}
}

func handleDatabaseChange(client *mpd.Client, picker strpick.Picker) {
	files, err := client.GetFiles()
	checkErr(err)
	if conf.Debug {
		log.Printf("Total files: %d\n", len(files))
	}

	newf := 0
	matchingf := 0
	newFiles := make(map[string]bool)
	for _, f := range files {
		if !strings.HasPrefix(f, conf.PathPrefix) {
			continue
		}

		matchingf++
		newFiles[f] = true

		if !lastFiles[f] {
			newf++
			err = picker.Add(f)
			checkErr(err)
		} else {
			delete(lastFiles, f)
		}
	}
	if conf.Debug {
		log.Printf("New files: %d, Existing files: %d, Removed files: %d\n",
			newf, matchingf-newf, len(lastFiles))
	}

	for f := range lastFiles {
		err = picker.Remove(f)
		checkErr(err)
	}
	lastFiles = newFiles
}

func watchLoop(watcher *mpd.Watcher, client *mpd.Client, picker strpick.Picker) {
	wg.Add(1)

	go func() {
		defer wg.Done()

		for true {
			select {
			case ev := <-watcher.Event:
				if conf.Debug {
					log.Println("Event: " + ev)
				}
				switch ev {
				case "player":
					handlePlayerChange(client, picker)
				case "database":
					handleDatabaseChange(client, picker)
				case "playlist":
					handlePlaylistChange(client)
				}
			case <-closeChan:
				return
			}
		}
	}()
}
