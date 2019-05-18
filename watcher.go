package main

import (
	"errors"
	"log"
	"strconv"

	"github.com/awused/go-strpick/persistent"
	"github.com/fhs/gompd/mpd"
)

func addNextTrack(
	client *mpd.Client, picker persistent.Picker,
	currentSongIndex int, currentSongID int) (bool, error) {
	f, err := picker.Next()
	if err != nil {
		return false, err
	}

	for currentSongID == s.playlist[f] {
		if conf.Debug {
			log.Printf("Selected song [%s] but it was already playing\n", f)
		}
		sz, err := picker.Size()
		if err != nil {
			return false, err
		}
		if sz <= 1 {
			log.Println("No other songs are available to play")
			return false, nil
		}

		f, err = picker.Next()
		if err != nil {
			return false, err
		}
	}

	if conf.Debug {
		log.Println("Adding song " + f)
	}

	if id, ok := s.playlist[f]; ok {
		if conf.Debug {
			log.Println("Song is already on the playlist, moving")
		}

		return false, client.MoveID(id, currentSongIndex)
	}

	return true, client.Add(f)
}

func handlePlayerChange(client *mpd.Client, picker persistent.Picker) error {
	attrs, err := client.Status()
	if err != nil {
		return err
	}

	if s.lastStateAttrs["state"] == attrs["state"] &&
		s.lastStateAttrs["song"] == attrs["song"] &&
		s.lastStateAttrs["songid"] == attrs["songid"] &&
		s.lastStateAttrs["nextsongid"] == attrs["nextsongid"] {
		return nil
	}
	s.lastStateAttrs = attrs

	s.state = attrs["state"]
	if conf.Debug {
		log.Println("state: " + s.state)
	}

	if s.state != "play" {
		return nil
	}

	currentSongIndex, err := strconv.Atoi(attrs["song"])
	if err != nil {
		return err
	}
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
		if err != nil {
			return err
		}
		if sz == 0 {
			log.Println("No valid files to add to end of playlist")
			return nil
		}

		currentSongID, err := strconv.Atoi(attrs["songid"])
		if err != nil {
			return err
		}
		addedNewTrack, err =
			addNextTrack(client, picker, currentSongIndex, currentSongID)
		if err != nil {
			return err
		}
	} else if conf.Debug {
		log.Printf(
			"Current song [%s], next song [%s]",
			attrs["songid"], attrs["nextsongid"])
	}

	if conf.KeepLast >= 0 && (hasNextSong || addedNewTrack) {
		if currentSongIndex > conf.KeepLast {
			end := currentSongIndex - conf.KeepLast
			if conf.Debug {
				log.Printf("Removing songs [%d, %d)\n", 0, end)
			}
			err := client.Delete(0, end)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func handlePlaylistChange(client *mpd.Client, picker persistent.Picker) error {
	tracks, err := client.PlaylistInfo(-1, -1)
	if err != nil {
		return err
	}

	s.playlist = make(map[string]int)
	for _, t := range tracks {
		songid, err := strconv.Atoi(t["Id"])
		if err != nil {
			return err
		}

		s.playlist[t["file"]] = songid
	}

	if len(tracks) == 0 {
		if conf.Debug {
			log.Println("Playlist empty, adding new track")
		}

		_, err =
			addNextTrack(client, picker, -1, -1)
		if err != nil {
			return err
		}
	}

	return nil
}

func handleDatabaseChange(client *mpd.Client, picker persistent.Picker) error {
	files, err := client.GetFiles()
	if err != nil {
		return err
	}
	if conf.Debug {
		log.Printf("Total files: %d\n", len(files))
	}

	newf := 0
	matchingf := 0
	newFiles := make(map[string]bool)
	toAdd := make([]string, 0)
	for _, f := range files {
		if !pathRegex.MatchString(f) {
			continue
		}

		matchingf++
		newFiles[f] = true

		if !s.lastFiles[f] {
			newf++
			toAdd = append(toAdd, f)
		} else {
			delete(s.lastFiles, f)
		}
	}

	// AddAll Gives better behaviour when new songs are encountered during
	// initial load
	err = picker.AddAll(toAdd)
	if err != nil {
		return err
	}

	if conf.Debug {
		log.Printf("New files: %d, Existing files: %d, Removed files: %d\n",
			newf, matchingf-newf, len(s.lastFiles))
	}

	for f := range s.lastFiles {
		err = picker.SoftRemove(f)
		if err != nil {
			return err
		}
	}
	s.lastFiles = newFiles
	return nil
}

func handleOptionsChange(client *mpd.Client) error {
	attrs, err := client.Status()
	if err != nil {
		return err
	}

	if conf.Debug {
		log.Printf("Status: %s", attrs)
	}

	if conf.DisableRepeat && attrs["repeat"] == "1" {
		return client.Repeat(false)
	}

	return nil
}

func watchLoop(watcher *mpd.Watcher, client *mpd.Client, picker persistent.Picker) {
	wg.Add(1)

	go func() {
		var err error
		defer wg.Done()

		for true {
			select {
			case ev := <-watcher.Event:
				if conf.Debug {
					log.Println("Event: " + ev)
				}
				switch ev {
				case "player":
					err = handlePlayerChange(client, picker)
				case "database":
					err = handleDatabaseChange(client, picker)
				case "playlist":
					err = handlePlaylistChange(client, picker)
				case "options":
					err = handleOptionsChange(client)
				case "":
					// Closed channel or garbage event
					err = errors.New("Watcher.Event channel closed unexpectedly")
				}
			case <-closeChan:
				return
			}
			if err != nil {
				sendError(err)
				return
			}
		}
	}()
}
