use lazylab::config::Token;
use lazylab::gitlab::Client;
use url::Url;

pub const TOKEN: &str = "glpat-abcdefghijklmnopqrst";

pub fn client_for(host: &str) -> Client {
    Client::new(&Url::parse(host).unwrap(), &Token::new(TOKEN)).unwrap()
}
