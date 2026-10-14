use lazylab::gitlab::Status;
use lazylab::tui::panel::{Row, block, folded_rule, label_style, light_corners, marked_line};
use lazylab::tui::text::{clamp_spans, fit, spans_width};
use lazylab::tui::theme::ROSE_PINE as T;
use lazylab::tui::{mark, text};
use ratatui::buffer::Buffer;
use ratatui::layout::Rect;
use ratatui::style::{Color, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::Widget;
use unicode_width::UnicodeWidthStr;

fn draw_line(line: Line<'static>, width: u16) -> Buffer {
    let mut buffer = Buffer::empty(Rect::new(0, 0, width, 1));
    line.render(buffer.area, &mut buffer);
    buffer
}

fn row_text(buffer: &Buffer, y: u16) -> String {
    (0..buffer.area.width)
        .map(|x| buffer[(x, y)].symbol())
        .collect()
}

fn project_row(current: bool) -> Row {
    Row {
        left: vec![Span::styled("acme/api", label_style(current, &T))],
        right: vec![Span::styled("3m", T.dim())],
    }
}

#[test]
fn the_current_row_wears_a_gold_bracket_pair_around_its_iris_label() {
    // Given: the current row of a list 20 cells wide, drawn with the selection fill.
    let line = marked_line(project_row(true), 20, true, true, &T);

    // When: it is drawn.
    let buffer = draw_line(line, 20);

    // Then: gold brackets close the row at both edges, and the label between them is iris.
    assert_eq!(row_text(&buffer, 0), "[acme/api        3m]");
    for x in [0, 19] {
        assert_eq!(buffer[(x, 0)].fg, T.gold, "bracket at {x}");
    }
    assert_eq!(buffer[(1, 0)].fg, T.iris);
    // And: the fill colours the background only, so it leaves the gold and iris pair intact.
    assert_eq!(buffer[(0, 0)].bg, T.hl_low);
}

#[test]
fn a_row_that_is_not_current_keeps_its_label_in_the_same_column() {
    // Given: the same row when the cursor is elsewhere.
    let line = marked_line(project_row(false), 20, false, true, &T);

    // When: it is drawn.
    let buffer = draw_line(line, 20);

    // Then: blank cells hold the bracket places, so the label does not move as the cursor does.
    assert_eq!(row_text(&buffer, 0), " acme/api        3m ");
    assert_eq!(buffer[(1, 0)].fg, T.text);
    assert_eq!(buffer[(0, 0)].bg, Color::Reset);
}

#[test]
fn a_row_fills_its_width_exactly_at_every_width() {
    // Given: a row whose label and readout are both longer than a narrow list.
    let row = || Row {
        left: vec![Span::raw("acme/platform/infrastructure/api")],
        right: vec![Span::raw("updated 3 minutes ago")],
    };

    // When: it is drawn at every width from the two bracket cells up.
    for width in 2..=60 {
        let line = marked_line(row(), width, true, false, &T);

        // Then: it is never wider or narrower than the list, so no border is pushed out.
        assert_eq!(spans_width(&line.spans), width as usize, "width {width}");
    }
}

#[test]
fn a_folded_pane_names_itself_and_its_selection_in_one_rule() {
    // Given: the jobs pane folded to one row, with the build job selected.
    let rule = folded_rule("jobs", vec![Span::raw("build")], 30, &T);

    // When: it is drawn.
    let buffer = draw_line(rule, 30);

    // Then: the rule reads as the pane and its selection, and the line runs to the edge.
    assert_eq!(row_text(&buffer, 0), "─ jobs · build ───────────────");
}

#[test]
fn a_folded_rule_leaves_one_gap_after_its_label_at_every_width() {
    // Given: a folded pane whose label and selection are long.
    for width in 2..=60u16 {
        // When: the rule is drawn at that width.
        let rule = folded_rule(
            "merge requests",
            vec![Span::raw("Add the login form")],
            width,
            &T,
        );
        let drawn: String = rule.spans.iter().map(|s| s.content.as_ref()).collect();

        // Then: it is exactly as wide as the pane.
        assert_eq!(drawn.width(), width as usize, "width {width}: {drawn:?}");
        // And: one space parts the label from the line, never two and never none.
        let label = drawn
            .trim_end_matches('─')
            .strip_suffix(' ')
            .unwrap_or_else(|| panic!("width {width}: no gap in {drawn:?}"));
        assert!(
            !label.is_empty() && !label.ends_with(' '),
            "width {width}: {drawn:?}"
        );
    }
}

#[test]
fn a_frame_cuts_its_title_into_a_square_top_rule() {
    // Given: a pipelines frame 24 cells wide.
    let mut buffer = Buffer::empty(Rect::new(0, 0, 24, 3));

    // When: it is drawn.
    block("pipelines", false, &T).render(buffer.area, &mut buffer);

    // Then: the title sits in the top rule after one dash, and every corner is square.
    assert_eq!(row_text(&buffer, 0), "┌─ pipelines ──────────┐");
    assert_eq!(row_text(&buffer, 2), "└──────────────────────┘");
}

#[test]
fn the_focused_region_lights_two_opposite_corners_in_gold() {
    // Given: a framed region 10 by 4 cells.
    let area = Rect::new(0, 0, 10, 4);
    let mut buffer = Buffer::empty(area);
    block("jobs", false, &T).render(area, &mut buffer);

    // When: its corners are lit.
    light_corners(&mut buffer, area, &T);

    // Then: the top left and bottom right corners turn gold, and the rest of the frame does not.
    let lit = [(0, 0), (1, 0), (0, 1), (9, 3), (8, 3), (9, 2)];
    for (x, y) in lit {
        assert_eq!(buffer[(x, y)].fg, T.gold, "({x}, {y})");
    }
    for (x, y) in [(9, 0), (0, 3), (5, 3)] {
        assert_eq!(buffer[(x, y)].fg, T.muted, "({x}, {y})");
    }
}

#[test]
fn a_region_too_small_for_corners_is_left_as_it_is() {
    // Given: a region one cell wide.
    let area = Rect::new(0, 0, 1, 3);
    let mut buffer = Buffer::empty(area);
    let before = buffer.clone();

    // When: its corners are lit.
    light_corners(&mut buffer, area, &T);

    // Then: nothing changes.
    assert_eq!(buffer, before);
}

const STATUSES: [Status; 15] = [
    Status::Created,
    Status::WaitingForResource,
    Status::WaitingForCallback,
    Status::Preparing,
    Status::Pending,
    Status::Scheduled,
    Status::Running,
    Status::Success,
    Status::Failed,
    Status::Canceled,
    Status::Canceling,
    Status::Skipped,
    Status::Manual,
    Status::Blocked,
    Status::Unknown,
];

#[test]
fn every_status_glyph_takes_one_cell_in_any_locale() {
    // Given: every status GitLab reports, at every frame of the spinner.
    for status in STATUSES {
        for tick in 0..20 {
            // When: its glyph is chosen.
            let (glyph, _) = T.status(status, tick);

            // Then: it is one cell wide, even where a CJK locale widens ambiguous glyphs.
            assert_eq!(
                (glyph.width(), glyph.width_cjk()),
                (1, 1),
                "{status:?} {glyph}"
            );
        }
    }
}

#[test]
fn statuses_that_mean_the_same_to_a_reader_share_a_glyph() {
    // Given: the statuses of a job that has not started, and of a cancel in progress.
    let glyph = |status| T.status(status, 0).0;

    // Then: every waiting status looks like pending, and a cancel in progress looks canceled.
    for waiting in [
        Status::Created,
        Status::WaitingForResource,
        Status::WaitingForCallback,
        Status::Preparing,
        Status::Scheduled,
    ] {
        assert_eq!(glyph(waiting), glyph(Status::Pending), "{waiting:?}");
    }
    assert_eq!(glyph(Status::Canceling), glyph(Status::Canceled));
    // And: a running job animates, so it changes from one tick to the next.
    assert_ne!(
        T.status(Status::Running, 0).0,
        T.status(Status::Running, 1).0
    );
}

#[test]
fn text_is_cut_to_its_cells_with_an_ellipsis() {
    // Given: a short name, a long name, a name of double-width characters, and no room at all.
    // Then: a name that fits is kept, and a longer one is cut to the cells with one ellipsis.
    assert_eq!(fit("api", 8), "api");
    assert_eq!(fit("acme/platform/api", 8), "acme/pl…");
    let wide = fit("日本語のプロジェクト", 5);
    assert!(wide.width() <= 5 && wide.ends_with('…'), "{wide}");
    assert_eq!(fit("api", 0), "");
}

#[test]
fn styled_text_is_cut_once_and_keeps_each_style() {
    // Given: a gold word followed by an iris word, with room for six cells.
    let spans = vec![
        Span::styled("abc", Style::new().fg(T.gold)),
        Span::styled("defgh", Style::new().fg(T.iris)),
    ];

    // When: it is fitted.
    let fitted = clamp_spans(spans, 6);

    // Then: the first word stays whole and gold, and the cut falls in the iris word.
    let parts: Vec<_> = fitted
        .iter()
        .map(|s| (s.content.as_ref(), s.style.fg))
        .collect();
    assert_eq!(parts, [("abc", Some(T.gold)), ("de…", Some(T.iris))]);
    assert_eq!(text::spans_width(&fitted), 6);
}

#[test]
fn every_row_of_the_mark_is_the_same_width() {
    // When: the lockup is laid out.
    let lines = mark::lines(&T);

    // Then: each row is as wide as the mark, so the right brace stands in one column.
    for (row, line) in lines.iter().enumerate() {
        assert_eq!(spans_width(&line.spans), mark::WIDTH, "row {row}");
    }
}
