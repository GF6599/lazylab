use lazylab::tui::app::{App, Cmd, Msg, Overlay};
use lazylab::tui::demo;
use lazylab::tui::form::{Field, Purpose};
use lazylab::tui::view::{Context, draw};
use ratatui::Terminal;
use ratatui::backend::TestBackend;
use ratatui::buffer::Buffer;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

const PROJECT: u64 = 101;
/// Three ticks of 100 ms, the wait before a moved cursor loads.
const SETTLE_TICKS: usize = 3;

fn now() -> OffsetDateTime {
    OffsetDateTime::parse("2026-01-15T12:00:00Z", &Rfc3339).unwrap()
}

fn answer(app: &mut App, mut commands: Vec<Cmd>) {
    while let Some(command) = commands.pop() {
        if let Some(msg) = demo::answer(command, now()) {
            commands.extend(app.update(msg));
        }
    }
}

fn loaded() -> App {
    let mut app = App::new(30);
    let commands = app.start();
    answer(&mut app, commands);
    app
}

fn key(app: &mut App, code: KeyCode) -> Vec<Cmd> {
    app.update(Msg::Key(KeyEvent::new(code, KeyModifiers::NONE)))
}

fn press(app: &mut App, c: char) -> Vec<Cmd> {
    key(app, KeyCode::Char(c))
}

fn ctrl(app: &mut App, c: char) -> Vec<Cmd> {
    app.update(Msg::Key(KeyEvent::new(
        KeyCode::Char(c),
        KeyModifiers::CONTROL,
    )))
}

fn type_text(app: &mut App, text: &str) -> Vec<Cmd> {
    text.chars().flat_map(|c| press(app, c)).collect()
}

fn ticks(app: &mut App, n: usize) -> Vec<Cmd> {
    (0..n).flat_map(|_| app.update(Msg::Tick)).collect()
}

fn searches(commands: &[Cmd]) -> Vec<String> {
    (commands.iter())
        .filter_map(|c| match c {
            Cmd::LoadBranches { project, search } if *project == PROJECT => Some(search.clone()),
            Cmd::LoadBranches { .. } => panic!("a search in another project: {c:?}"),
            _ => None,
        })
        .collect()
}

fn creates(commands: &[Cmd]) -> Vec<Cmd> {
    (commands.iter())
        .filter(|c| matches!(c, Cmd::CreateMergeRequest { .. }))
        .cloned()
        .collect()
}

/// The create form on the merge requests pane of the first demo project.
fn opened() -> App {
    let mut app = loaded();
    press(&mut app, '4');
    press(&mut app, 'N');
    app
}

/// The create form with the branches its first search found.
fn listed() -> App {
    let mut app = loaded();
    press(&mut app, '4');
    let asked = press(&mut app, 'N');
    answer(&mut app, asked);
    app
}

/// Types `text` into the field in use, and lets the search for it settle and answer.
fn search(app: &mut App, text: &str) {
    type_text(app, text);
    let asked = ticks(app, SETTLE_TICKS);
    answer(app, asked);
}

fn matches(app: &App) -> Option<(String, Vec<String>)> {
    let fields = app.form.as_ref().unwrap().merge_request.as_ref().unwrap();
    (fields.matches.as_ref()).map(|m| (m.search.clone(), m.branches.clone()))
}

fn fields(app: &App) -> (String, String, String) {
    let fields = app.form.as_ref().unwrap().merge_request.as_ref().unwrap();
    (
        fields.source.clone(),
        fields.target.clone(),
        fields.title.clone(),
    )
}

fn current(app: &App) -> Option<Field> {
    app.form.as_ref().unwrap().current()
}

fn render(app: &App) -> Buffer {
    let mut terminal = Terminal::new(TestBackend::new(120, 18)).unwrap();
    let context = Context {
        host: "gitlab.example.com".into(),
        now: now(),
    };
    terminal.draw(|frame| draw(frame, app, &context)).unwrap();
    terminal.backend().buffer().clone()
}

fn screen(app: &App) -> String {
    let buffer = render(app);
    (0..buffer.area.height)
        .map(|y| {
            let row: String = (0..buffer.area.width)
                .map(|x| buffer[(x, y)].symbol())
                .collect();
            row.trim_end().to_string()
        })
        .collect::<Vec<_>>()
        .join("\n")
}

fn status_bar(app: &App) -> String {
    screen(app).lines().last().unwrap().to_string()
}

#[test]
fn n_on_the_merge_requests_pane_opens_the_form_with_the_default_branch_as_the_target() {
    // Given: the merge requests pane of the first demo project, whose default branch is main.
    // When: N is pressed.
    let app = opened();

    // Then: the form opens on an empty source branch, with main as the target and no title.
    assert_eq!(app.overlay, Overlay::Form);
    let form = app.form.as_ref().unwrap();
    assert_eq!(form.purpose, Purpose::MergeRequest { project: PROJECT });
    assert_eq!(fields(&app), (String::new(), "main".into(), String::new()));
    assert_eq!(current(&app), Some(Field::Source));
}

#[test]
fn n_keeps_its_own_meaning_on_every_other_pane() {
    // Given: the demo data.
    let mut app = loaded();

    // When: N is pressed on the projects pane. Then: no form opens.
    press(&mut app, 'N');
    assert_eq!(app.overlay, Overlay::None);

    // When: N is pressed on the pipelines pane. Then: the run form opens.
    press(&mut app, '2');
    press(&mut app, 'N');
    assert_eq!(app.form.as_ref().unwrap().title(), "run pipeline");
}

#[test]
fn enter_moves_through_the_branches_and_sends_from_the_title() {
    // Given: the create form.
    let mut app = opened();

    // When: a source branch is typed and enter is pressed, and enter is pressed on the target.
    type_text(&mut app, "feat/sso-login");
    let first = key(&mut app, KeyCode::Enter);
    let second = key(&mut app, KeyCode::Enter);

    // Then: nothing is sent, and the title is in use.
    assert_eq!(creates(&[first, second].concat()), []);
    assert_eq!(current(&app), Some(Field::Title));

    // When: a title is typed, and enter is pressed.
    type_text(&mut app, "Add SSO login");
    let sent = key(&mut app, KeyCode::Enter);

    // Then: the merge request is created from the typed branch into main, and the form closes.
    assert_eq!(
        creates(&sent),
        [Cmd::CreateMergeRequest {
            project: PROJECT,
            source: "feat/sso-login".into(),
            target: "main".into(),
            title: "Add SSO login".into(),
        }]
    );
    assert_eq!(app.overlay, Overlay::None);
    assert!(app.form.is_none());
}

#[test]
fn ctrl_s_sends_from_any_field_and_esc_closes_the_form() {
    // Given: a title typed after a tab back from the source branch, which is then typed.
    let mut app = opened();
    key(&mut app, KeyCode::BackTab);
    type_text(&mut app, "Fix the flaky checkout");
    key(&mut app, KeyCode::Tab);
    type_text(&mut app, "fix/flaky-e2e");

    // When: ctrl+s is pressed on the source branch.
    let sent = ctrl(&mut app, 's');

    // Then: the merge request is created.
    assert_eq!(
        creates(&sent),
        [Cmd::CreateMergeRequest {
            project: PROJECT,
            source: "fix/flaky-e2e".into(),
            target: "main".into(),
            title: "Fix the flaky checkout".into(),
        }]
    );

    // When: the form opens again, and esc is pressed.
    let mut app = opened();
    let closed = key(&mut app, KeyCode::Esc);

    // Then: the form closes, and nothing is sent.
    assert_eq!(creates(&closed), []);
    assert_eq!(app.overlay, Overlay::None);
}

#[test]
fn the_key_bar_offers_n_on_the_merge_requests_pane() {
    // Given: the demo data on the merge requests pane.
    let mut app = loaded();
    press(&mut app, '4');

    // When: the screen is drawn. Then: the key bar offers N.
    let bar = status_bar(&app);
    assert!(bar.contains("N new"), "{bar}");
}

#[test]
fn the_create_form_opens_with_the_source_branch_marked() {
    // Given: N pressed on the merge requests pane.
    let app = opened();

    // Then: the source branch is marked and empty, and the target is main.
    insta::assert_snapshot!(screen(&app));
}

#[test]
fn the_create_form_shows_both_branches_and_the_title() {
    // Given: a source branch typed, main kept as the target, and a title typed.
    let mut app = opened();
    type_text(&mut app, "feat/runner-cache");
    key(&mut app, KeyCode::Enter);
    key(&mut app, KeyCode::Enter);
    type_text(&mut app, "Cache the runner image layers");

    // Then: the title is marked, after both branches.
    insta::assert_snapshot!(screen(&app));
}

#[test]
fn n_searches_every_branch_at_once() {
    // Given: the merge requests pane.
    let mut app = loaded();
    press(&mut app, '4');

    // When: N opens the form.
    let asked = press(&mut app, 'N');

    // Then: the branches are asked for with no search, so the empty source branch lists them.
    assert_eq!(searches(&asked), [""]);
}

#[test]
fn typing_in_a_branch_field_searches_once_the_keys_settle() {
    // Given: the form with every branch listed.
    let mut app = listed();

    // When: "feat" is typed, one tick apart.
    let mut asked = Vec::new();
    for c in "feat".chars() {
        asked.extend(press(&mut app, c));
        asked.extend(ticks(&mut app, 1));
    }

    // Then: nothing is asked for while the keys come.
    assert_eq!(searches(&asked), Vec::<String>::new());

    // When: the keys rest. Then: the branches are searched for the whole term, once.
    let settled = ticks(&mut app, SETTLE_TICKS);
    assert_eq!(searches(&settled), ["feat"]);
    assert_eq!(
        searches(&ticks(&mut app, SETTLE_TICKS)),
        Vec::<String>::new()
    );
}

#[test]
fn a_paste_into_a_branch_field_searches_once_it_settles() {
    // Given: the form with every branch listed.
    let mut app = listed();

    // When: a branch name is pasted, and the keys rest.
    app.update(Msg::Paste("fix/\n".into()));
    let settled = ticks(&mut app, SETTLE_TICKS);

    // Then: the field holds the name alone, and the branches are searched for it.
    assert_eq!(fields(&app).0, "fix/");
    assert_eq!(searches(&settled), ["fix/"]);
}

#[test]
fn a_late_answer_for_a_term_typed_over_is_dropped() {
    // Given: a search for "fe" out, and then "a" typed.
    let mut app = listed();
    type_text(&mut app, "fe");
    let asked = ticks(&mut app, SETTLE_TICKS);
    assert_eq!(searches(&asked), ["fe"]);
    press(&mut app, 'a');
    let shown = matches(&app);

    // When: the answer for "fe" arrives.
    answer(&mut app, asked);

    // Then: the form still shows what it showed before.
    assert_eq!(matches(&app), shown);

    // When: the search for "fea" settles and is answered.
    let asked = ticks(&mut app, SETTLE_TICKS);
    answer(&mut app, asked);

    // Then: the form shows the branches that match "fea".
    let (search, branches) = matches(&app).unwrap();
    assert_eq!(search, "fea");
    assert!(!branches.is_empty());
    assert!(branches.iter().all(|b| b.contains("fea")), "{branches:?}");
}

#[test]
fn an_answer_for_the_term_of_another_field_is_dropped() {
    // Given: the form with every branch listed, moved on to the target branch.
    let mut app = listed();
    key(&mut app, KeyCode::Tab);

    // When: an answer for the empty term of the source branch arrives.
    let late = Cmd::LoadBranches {
        project: PROJECT,
        search: String::new(),
    };
    app.update(demo::answer(late, now()).unwrap());

    // Then: the target branch shows no matches until its own search answers.
    assert_eq!(current(&app), Some(Field::Target));
    assert_eq!(matches(&app), None);
}

#[test]
fn down_and_enter_take_the_picked_branch_and_move_to_the_next_field() {
    // Given: the branches that match "feat".
    let mut app = listed();
    search(&mut app, "feat");
    let (_, branches) = matches(&app).unwrap();

    // When: down picks the second match, and enter is pressed.
    key(&mut app, KeyCode::Down);
    let commands = key(&mut app, KeyCode::Enter);

    // Then: the source branch is the second match, nothing is sent, and the target branch is in
    // use.
    assert_eq!(fields(&app).0, branches[1]);
    assert_eq!(creates(&commands), []);
    assert_eq!(current(&app), Some(Field::Target));

    // When: the keys rest. Then: the branches are searched for the target branch.
    assert_eq!(searches(&ticks(&mut app, SETTLE_TICKS)), ["main"]);
}

#[test]
fn the_create_form_lists_the_branches_that_match_the_search() {
    // Given: the branches that match "feat", with the second one picked.
    let mut app = listed();
    search(&mut app, "feat");
    key(&mut app, KeyCode::Down);

    // Then: the matches show under the fields, the picked one between gold brackets.
    insta::assert_snapshot!(screen(&app));
}
