use std::io::Error;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;

use clap::StructOpt;
use futures_util::StreamExt;
use once_cell::sync::Lazy;
use signal_hook::consts::{SIGHUP, SIGUSR1, TERM_SIGNALS};
use signal_hook::flag;
use signal_hook_tokio::Signals;
use tokio::select;


mod config;
mod connection;

#[derive(Debug, StructOpt)]
#[structopt(
    name = "mpd-shuffler",
    about = "Application to continuously shuffle your MPD library."
)]
pub struct Opt {
    #[structopt(short, long, parse(from_os_str))]
    /// Override the selected config.
    awconf: Option<PathBuf>,
}

pub static OPTIONS: Lazy<Opt> = Lazy::new(Opt::parse);
static CLOSED: Lazy<Arc<AtomicBool>> = Lazy::new(|| Arc::new(AtomicBool::new(false)));


#[tokio::main(flavor = "current_thread")]
async fn main() -> Result<(), Error> {
    let mut signals = Signals::new(TERM_SIGNALS)?;
    let handle = signals.handle();
    for sig in TERM_SIGNALS {
        flag::register_conditional_default(*sig, CLOSED.clone()).unwrap();
        flag::register(*sig, CLOSED.clone()).unwrap();
    }
    flag::register_conditional_default(SIGHUP, CLOSED.clone()).unwrap();
    flag::register(SIGHUP, CLOSED.clone()).unwrap();
    handle.add_signal(SIGHUP)?;
    handle.add_signal(SIGUSR1)?;

    'outer: while !CLOSED.load(Ordering::Relaxed) {
        match connection::run(&mut signals).await {
            Ok(_) => (),
            Err(e) => println!("Encountered error {:?}", e),
        }

        if CLOSED.load(Ordering::Relaxed) {
            break;
        }

        println!("Reconnecting in one minute");

        select! {
            sig = signals.next() => {
                match sig {
                    Some(SIGUSR1) => println!("Got SIGUSR1 while disconnected, reconnecting now"),
                    Some(_) => break 'outer,
                    None => unreachable!(),
                }
            },
            _ = tokio::time::sleep(Duration::from_secs(60)) => {}
        }
    }

    Ok(())
}
