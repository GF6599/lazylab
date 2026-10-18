mod common;

use std::io::{self, Write};
use std::net::TcpListener;
use std::sync::{Arc, Mutex};

use common::client_for;
use tracing::Level;
use tracing::subscriber::DefaultGuard;
use wiremock::matchers::{method, path};
use wiremock::{Mock, MockServer, ResponseTemplate};

#[derive(Clone, Default)]
struct Captured(Arc<Mutex<Vec<u8>>>);

impl Write for Captured {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        self.0.lock().unwrap().extend_from_slice(buf);
        Ok(buf.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

impl Captured {
    fn text(&self) -> String {
        String::from_utf8(self.0.lock().unwrap().clone()).unwrap()
    }
}

/// The events of this thread at `level` and above go to the answer until the guard drops.
fn capture(level: Level) -> (Captured, DefaultGuard) {
    let captured = Captured::default();
    let sink = captured.clone();
    let subscriber = tracing_subscriber::fmt()
        .with_writer(move || sink.clone())
        .with_ansi(false)
        .without_time()
        .with_max_level(level)
        .finish();
    (captured, tracing::subscriber::set_default(subscriber))
}

#[tokio::test]
async fn an_answered_request_logs_its_operation_path_status_and_duration_at_debug() {
    // Given: GitLab answers the bridges of a pipeline, and debug events are captured.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/7/pipelines/9/bridges"))
        .respond_with(ResponseTemplate::new(200).set_body_string("[]"))
        .mount(&server)
        .await;
    let (log, _guard) = capture(Level::DEBUG);

    // When: the bridges are listed.
    client_for(&server.uri())
        .list_pipeline_bridges(7, 9)
        .await
        .unwrap();

    // Then: one debug line names the request, its answer and how long it took.
    let text = log.text();
    let line = (text.lines())
        .find(|line| line.contains("list pipeline bridges"))
        .unwrap_or_else(|| panic!("no line for the request in:\n{text}"));
    assert!(line.contains("DEBUG"), "{line}");
    assert!(
        line.contains("path=/api/v4/projects/7/pipelines/9/bridges"),
        "{line}"
    );
    assert!(line.contains("status=200"), "{line}");
    assert!(line.contains("elapsed_ms="), "{line}");
}

#[tokio::test]
async fn a_request_that_gets_no_answer_logs_a_warning_with_its_duration() {
    // Given: a port that nothing listens on, and only warnings and errors captured.
    let closed = TcpListener::bind("127.0.0.1:0").unwrap().local_addr().unwrap();
    let (log, _guard) = capture(Level::WARN);

    // When: the bridges are listed there.
    let result = client_for(&format!("http://{closed}"))
        .list_pipeline_bridges(7, 9)
        .await;

    // Then: the call fails, and a warning names the request, how long it took and why.
    assert!(result.is_err());
    let text = log.text();
    let line = (text.lines())
        .find(|line| line.contains("list pipeline bridges"))
        .unwrap_or_else(|| panic!("no line for the request in:\n{text}"));
    assert!(line.contains("WARN"), "{line}");
    assert!(
        line.contains("path=/api/v4/projects/7/pipelines/9/bridges"),
        "{line}"
    );
    assert!(line.contains("elapsed_ms="), "{line}");
    assert!(line.contains("error="), "{line}");
}
