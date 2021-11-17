mpd-shuffler
============

Randomly shuffles your MPD library in a way that makes less recently played songs more likely to be played.

# About

This uses [aw-shuffle](https://github.com/awused/aw-shuffle) to randomly shuffle your MPD library, continuously, forever. Compared to ashuffle it does not use a small sliding window of recently played songs, making it more likely that every song will be picked in a reasonable amount of time.

# Usage

`cargo install --git http://github.com/awused/mpd-shuffler`

Fill in mpd-shuffler.toml and copy it to your choice of $HOME/.config/mpd-shuffler/mpd-shuffler.toml, $HOME/.mpd-shuffler.toml, /usr/local/etc/mpd-shuffler.toml, or /usr/etc/mpd-shuffler.toml.


### Cleaning

The database can be cleaned of songs that have been deleted or renamed by sending sending SIGUSR1 to the process. This is not necessary for normal operation and is only useful for those who want to inspect the database as a curiosity.
