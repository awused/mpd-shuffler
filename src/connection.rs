use std::collections::{HashMap, HashSet};
use std::num::ParseIntError;
use std::sync::Arc;
use std::time::Duration;

use aw_shuffle::persistent::rocksdb::Shuffler;
use aw_shuffle::persistent::{Options, PersistentShuffler};
use aw_shuffle::AwShuffler;
use futures_util::StreamExt;
use mpd_protocol::{response, AsyncConnection, Command, MpdProtocolError, Response};
use signal_hook::consts::SIGUSR1;
use signal_hook_tokio::Signals;
use tokio::io::{AsyncRead, AsyncWrite};
use tokio::net::{lookup_host, TcpStream, UnixStream};
use tokio::select;
use tokio::time::{interval, MissedTickBehavior};

use crate::config::CONFIG;


trait MpdIo: AsyncRead + AsyncWrite + Unpin {}
impl<M: AsyncRead + AsyncWrite + Unpin> MpdIo for M {}

// We assume anything dealing with MPD is allowed to fail, but anything involving the shuffler must
// succeed.
#[derive(Debug)]
pub(super) enum Error {
    IO(std::io::Error),
    Mpd(MpdProtocolError),
    MpdResponse(response::Error),
    ParseInt(ParseIntError),
    UnexpectedNone,
}

type Result<T> = std::result::Result<T, Error>;
type Client = AsyncConnection<Box<dyn MpdIo>>;

#[derive(Default, Debug)]
struct PlayerState {
    state: String,
    song: String,
    song_id: String,
    next_song_id: String,
    playlist: HashMap<String, String>,
}

pub(super) async fn run(signals: &mut Signals) -> Result<()> {
    let mut ping = interval(Duration::from_secs(CONFIG.mpd_timeout));
    ping.set_missed_tick_behavior(MissedTickBehavior::Burst);

    let idle_command = Command::new("idle")
        .argument("database")
        .argument("player")
        .argument("playlist")
        .argument("options")
        .argument("mixer");

    // Dynamic dispatch is fine to avoid needing to duplicate any code.
    let client: Box<dyn MpdIo> = if lookup_host(&CONFIG.mpd_address).await.is_ok() {
        Box::new(TcpStream::connect(&CONFIG.mpd_address).await?)
    } else {
        Box::new(UnixStream::connect(&CONFIG.mpd_address).await?)
    };
    let mut client = AsyncConnection::connect(client).await?;


    let files = get_files(&mut client).await?.collect();
    let options = Options::default().keep_unrecognized(true);
    let mut shuffler = Shuffler::new(&CONFIG.database, options, Some(files)).unwrap();

    let mut ps = PlayerState::default();

    options_change(&mut client).await?;
    ps.playlist_change(&mut client, &mut shuffler).await?;
    ps.player_change(&mut client, &mut shuffler).await?;

    client.send(idle_command.clone()).await?;
    loop {
        select! {
            idle = client.receive() => {
                let resp = idle?.ok_or(Error::UnexpectedNone)?;

                for (_, v) in flatten_response(resp)? {
                    match &*v {
                        "database" => update_files(&mut client, &mut shuffler).await?,
                        "player" => ps.player_change(&mut client, &mut shuffler).await?,
                        "playlist" => ps.playlist_change(&mut client, &mut shuffler).await?,
                        "options" | "mixer" => options_change(&mut client).await?,
                        _ => ()
                    }
                }

                client.send(idle_command.clone()).await?;
            }
            sig = signals.next() => {
                match sig {
                    Some(SIGUSR1) => {
                        println!("Got SIGUSR1, cleaning database");
                        // TODO -- into_values()
                        let files: Vec<_> = shuffler.values().into_iter().cloned().collect();
                        shuffler.close().unwrap();

                        let options = Options::default().keep_unrecognized(false);
                        let mut temp_shuffler =
                            Shuffler::new(&CONFIG.database, options, Some(files.clone())).unwrap();
                        temp_shuffler.compact().unwrap();
                        temp_shuffler.close().unwrap();

                        let options = Options::default().keep_unrecognized(true);
                        shuffler = Shuffler::new(&CONFIG.database, options, Some(files)).unwrap();
                    },
                    Some(_) => {
                        client.send(Command::new("noidle")).await?;
                        return Ok(())
                    },
                    None => unreachable!(),
                }
            },
        }
    }
}

impl PlayerState {
    async fn player_change(
        &mut self,
        client: &mut Client,
        shuffler: &mut Shuffler<String>,
    ) -> Result<()> {
        client.send(Command::new("status")).await?;
        let resp = client.receive().await?.ok_or(Error::UnexpectedNone)?;
        let mut resp: HashMap<_, _> = flatten_response(resp)?.collect();

        if self.state == resp.get("state").map_or("", |s| &*s)
            && self.song == resp.get("song").map_or("", |s| &*s)
            && self.song_id == resp.get("songid").map_or("", |s| &*s)
            && self.next_song_id == resp.get("nextsongid").map_or("", |s| &*s)
        {
            return Ok(());
        }
        self.state = resp.remove("state").unwrap_or_else(|| "".to_string());
        self.song = resp.remove("song").unwrap_or_else(|| "".to_string());
        self.song_id = resp.remove("songid").unwrap_or_else(|| "".to_string());
        self.next_song_id = resp.remove("nextsongid").unwrap_or_else(|| "".to_string());

        if self.state != "play" {
            return Ok(());
        }

        let added = self.maybe_add_next(client, shuffler).await?;

        if added || !self.next_song_id.is_empty() {
            if let Some(keep) = CONFIG.keep_last {
                let song_index: u32 = self.song.parse()?;
                if song_index > keep {
                    let del = "0:".to_owned() + &(song_index - keep).to_string();
                    send_recv_simple(client, Command::new("delete").argument(del)).await?;
                }
            }
        }

        Ok(())
    }

    async fn playlist_change(
        &mut self,
        client: &mut Client,
        shuffler: &mut Shuffler<String>,
    ) -> Result<()> {
        client.send(Command::new("playlistinfo")).await?;
        let resp = client.receive().await?.ok_or(Error::UnexpectedNone)?;
        let resp = flatten_response(resp)?;

        self.playlist.clear();

        // This is just a list of tuples
        let mut file = None;
        let mut id = None;
        for (k, v) in resp {
            if &*k == "file" {
                assert!(file.is_none(), "Mismatched playlist, unmatched file: {:?}", file);
                file = Some(v);
            } else if &*k == "Id" {
                assert!(id.is_none(), "Mismatched playlist, unmatched id:{:?}", id);
                id = Some(v);
            }

            if file.is_some() && id.is_some() {
                self.playlist.insert(file.take().unwrap(), id.take().unwrap());
            }
        }

        // If either are still set then one was unpaired.
        assert!(
            file.is_none() && id.is_none(),
            "Mismatched playlist, unmatched file: {:?} or id: {:?}",
            file,
            id
        );

        if self.playlist.is_empty() {
            self.maybe_add_next(client, shuffler).await?;
        }
        Ok(())
    }

    async fn maybe_add_next(
        &mut self,
        client: &mut Client,
        shuffler: &mut Shuffler<String>,
    ) -> Result<bool> {
        if !self.next_song_id.is_empty() || shuffler.size() == 0 {
            return Ok(false);
        }

        let (random_song, random_id) = loop {
            let random_song = shuffler.next().unwrap().unwrap();
            // It would be very, very unlikely for this to happen more than once.
            let id = self.playlist.get(random_song);
            if let Some(id) = id {
                if id == &self.song_id {
                    if shuffler.size() == 1 {
                        println!("No other songs are available to play");
                        return Ok(false);
                    }
                    continue;
                }
            }
            break (random_song, id);
        };

        if let Some(id) = random_id {
            send_recv_simple(
                client,
                Command::new("moveid").argument(id.clone()).argument(self.song.clone()),
            )
            .await?;
            Ok(false)
        } else {
            send_recv_simple(client, Command::new("add").argument(random_song.clone())).await?;
            Ok(true)
        }
    }
}

fn flatten_response(resp: Response) -> Result<impl Iterator<Item = (Arc<str>, String)>> {
    if resp.is_error() {
        resp.single_frame()?;
        unreachable!()
    }

    let resp = resp.into_iter().collect::<std::result::Result<Vec<_>, _>>()?;
    Ok(resp.into_iter().flat_map(IntoIterator::into_iter))
}

async fn send_recv_simple(client: &mut Client, cmd: Command) -> Result<()> {
    client.send(cmd).await?;
    client.receive().await?.ok_or(Error::UnexpectedNone)?.single_frame()?;
    Ok(())
}

async fn get_files(client: &mut Client) -> Result<impl Iterator<Item = String>> {
    client.send(Command::new("list").argument("file")).await?;

    let resp = client.receive().await?.ok_or(Error::UnexpectedNone)?;
    Ok(flatten_response(resp)?
        .filter(|(k, s)| {
            &**k == "file" && CONFIG.song_regex.as_ref().map_or(true, |re| re.is_match(s))
        })
        .map(|(_, s)| s))
}

async fn update_files(client: &mut Client, shuffler: &mut Shuffler<String>) -> Result<()> {
    let mut new_files = get_files(client).await?.collect::<HashSet<_>>();
    let mut remove_files = HashSet::new();

    let existing_files = shuffler.values().into_iter().collect::<HashSet<_>>();

    for ef in existing_files {
        if !new_files.remove(ef) {
            // Unless churn is very large this will introduce minimal cloning.
            remove_files.insert(ef.clone());
        }
    }

    for nf in new_files {
        shuffler.load(nf).unwrap();
    }

    for rf in remove_files {
        shuffler.soft_remove(&rf).unwrap();
    }

    Ok(())
}

async fn options_change(client: &mut Client) -> Result<()> {
    client.send(Command::new("status")).await?;
    let resp = client.receive().await?.ok_or(Error::UnexpectedNone)?;

    for (k, v) in flatten_response(resp)? {
        if &*k == "repeat" && &v == "1" && CONFIG.disable_repeat {
            send_recv_simple(client, Command::new("repeat").argument("0")).await?;
        }
        if &*k == "volume" && &v != "100" && CONFIG.lock_volume {
            send_recv_simple(client, Command::new("setvol").argument("100")).await?;
        }
        if &*k == "xfade" && &v != "0" && CONFIG.disable_crossfade {
            send_recv_simple(client, Command::new("crossfade").argument("0")).await?;
        }
    }

    Ok(())
}

impl From<std::io::Error> for Error {
    fn from(e: std::io::Error) -> Self {
        Self::IO(e)
    }
}

impl From<MpdProtocolError> for Error {
    fn from(e: MpdProtocolError) -> Self {
        Self::Mpd(e)
    }
}

impl From<response::Error> for Error {
    fn from(e: response::Error) -> Self {
        Self::MpdResponse(e)
    }
}

impl From<ParseIntError> for Error {
    fn from(e: ParseIntError) -> Self {
        Self::ParseInt(e)
    }
}
