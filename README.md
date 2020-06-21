mpd-shuffler
============

Randomly shuffles your MPD library in a way that makes less recently played songs more likely to be played.


# Usage

`go get -u github.com/awused/mpd-shuffler`

Fill in mpd-shuffler.toml and copy it to your choice of /usr/local/etc/mpd-shuffler.toml, /usr/etc/mpd-shuffler.toml, $GOBIN/mpd-shuffler.toml, or $HOME/.mpd-shuffler.toml.


### Cleaning

The database can be cleaned of songs that have been deleted or renamed by sending sending SIGUSR1 to the process. This is not necessary for normal operation and is only useful for those who want to inspect the database as a curiosity.
