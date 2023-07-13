use std::path::PathBuf;

use once_cell::sync::Lazy;
use regex::Regex;
use serde::{Deserialize, Deserializer};

use crate::OPTIONS;

pub static CONFIG: Lazy<Config> = Lazy::new(|| {
    awconf::load_config::<Config>("mpd-shuffler", &OPTIONS.awconf).expect("Error loading config")
});

#[derive(Debug, Deserialize)]
pub struct Config {
    pub mpd_address: String,

    pub database: PathBuf,

    #[serde(default, deserialize_with = "de_regex")]
    pub song_regex: Option<Regex>,

    #[serde(default, deserialize_with = "negative_is_none")]
    pub keep_last: Option<u32>,

    #[serde(default)]
    pub disable_repeat: bool,

    #[serde(default)]
    pub lock_volume: bool,

    #[serde(default)]
    pub disable_crossfade: bool,
}

fn negative_is_none<'de, D>(deserializer: D) -> Result<Option<u32>, D::Error>
where
    D: Deserializer<'de>,
{
    let i = i64::deserialize(deserializer)?;
    if i < 0 { Ok(None) } else { Ok(Some(i as u32)) }
}

fn de_regex<'de, D>(deserializer: D) -> Result<Option<Regex>, D::Error>
where
    D: Deserializer<'de>,
{
    let re = String::deserialize(deserializer)?;
    if re.is_empty() {
        return Ok(None);
    }
    Regex::new(&re).map_or_else(
        |e| panic!("Unable to deserialize regular expression [{re}] {e}"),
        |v| Ok(Some(v)),
    )
}
