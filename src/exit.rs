//! Exit codes. Scripts that wrap lazylab read them to tell a failure worth retrying from one
//! that is not, so each value keeps its meaning across releases.

/// A bad setting, an unknown argument, or a request GitLab rejected for its content. clap exits
/// with 2 on a usage error, so a usage error is mapped here instead.
pub const GENERIC: u8 = 1;
/// Rate limited or a server error: safe to retry with a backoff.
pub const TRANSIENT: u8 = 2;
/// The token is missing, invalid or lacks the scope.
pub const UNAUTHORIZED: u8 = 3;
/// No response arrived: DNS, connection or TLS.
pub const NETWORK: u8 = 4;
