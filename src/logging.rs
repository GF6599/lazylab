//! Logs to stderr through `redact`.

use std::io::{self, Write};

use tracing_subscriber::fmt::MakeWriter;

/// Hands out writers that pass each formatted event through `redact`.
pub struct Redacted<M>(pub M);

impl<'a, M: MakeWriter<'a>> MakeWriter<'a> for Redacted<M> {
    type Writer = RedactingWriter<M::Writer>;

    fn make_writer(&'a self) -> Self::Writer {
        RedactingWriter(self.0.make_writer())
    }
}

pub struct RedactingWriter<W>(W);

impl<W: Write> Write for RedactingWriter<W> {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        self.0.write(buf)
    }

    fn flush(&mut self) -> io::Result<()> {
        self.0.flush()
    }
}

#[cfg(test)]
mod tests {
    use std::sync::{Arc, Mutex};

    use super::Redacted;

    #[derive(Clone, Default)]
    struct Captured(Arc<Mutex<Vec<u8>>>);

    impl std::io::Write for Captured {
        fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
            self.0.lock().unwrap().extend_from_slice(buf);
            Ok(buf.len())
        }

        fn flush(&mut self) -> std::io::Result<()> {
            Ok(())
        }
    }

    #[test]
    fn a_token_in_the_message_a_field_or_a_span_reaches_stderr_redacted() {
        let captured = Captured::default();
        let sink = captured.clone();
        let subscriber = tracing_subscriber::fmt()
            .with_writer(Redacted(move || sink.clone()))
            .with_ansi(false)
            .without_time()
            .finish();

        tracing::subscriber::with_default(subscriber, || {
            let span =
                tracing::info_span!("request", header = "PRIVATE-TOKEN: abcdefghijklmnopqrst");
            let _entered = span.enter();
            tracing::error!(
                err = "token glpat-secret123456789 is invalid",
                "failed with glpat-secrettoken123456"
            );
            tracing::error!(host = "gitlab.com", port = 443, "connection established");
        });

        let output = String::from_utf8(captured.0.lock().unwrap().clone()).unwrap();
        for secret in [
            "abcdefghijklmnopqrst",
            "glpat-secret123456789",
            "glpat-secrettoken123456",
        ] {
            assert!(!output.contains(secret), "{secret} leaked:\n{output}");
        }
        assert!(output.contains("PRIVATE-TOKEN: [REDACTED]"), "{output}");
        assert!(output.contains("host=\"gitlab.com\" port=443"), "{output}");
    }
}
