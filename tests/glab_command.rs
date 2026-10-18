use lazylab::glab_command::{Item, commands};
use url::Url;

fn lines(host: Option<&str>, project: &str, item: Item) -> Vec<String> {
    let host = host.map(|h| Url::parse(h).unwrap());
    let commands = commands(host.as_ref(), project, item);
    for command in &commands {
        assert!(!command.label.is_empty(), "{} has no label", command.line);
    }
    commands.into_iter().map(|c| c.line).collect()
}

#[test]
fn each_item_maps_to_the_glab_commands_of_the_go_release_in_order() {
    // Given: an item of each kind in acme/widgets on gitlab.com, with the commands Go gives it.
    let pipeline = |git_ref| Item::Pipeline { id: 4242, git_ref };
    let cases: [(Item, &[&str]); 6] = [
        (Item::Project, &["glab repo view acme/widgets"]),
        (
            pipeline("main"),
            &[
                "glab ci get -p 4242 -R acme/widgets",
                "glab ci view main -R acme/widgets",
                "glab ci list -R acme/widgets",
                "glab ci cancel pipeline 4242 -R acme/widgets",
            ],
        ),
        (
            pipeline("refs/merge-requests/57/head"),
            &[
                "glab ci get -p 4242 -R acme/widgets",
                "glab ci list -R acme/widgets",
                "glab ci cancel pipeline 4242 -R acme/widgets",
            ],
        ),
        (
            pipeline(""),
            &[
                "glab ci get -p 4242 -R acme/widgets",
                "glab ci list -R acme/widgets",
                "glab ci cancel pipeline 4242 -R acme/widgets",
            ],
        ),
        (
            Item::Job { id: 99821 },
            &[
                "glab ci trace 99821 -R acme/widgets",
                "glab ci retry 99821 -R acme/widgets",
                "glab ci cancel job 99821 -R acme/widgets",
            ],
        ),
        (
            Item::MergeRequest { iid: 42 },
            &[
                "glab mr view 42 -R acme/widgets",
                "glab mr diff 42 -R acme/widgets",
            ],
        ),
    ];
    for (item, want) in cases {
        // When / Then: the commands are the same, in the same order.
        assert_eq!(
            lines(Some("https://gitlab.com"), "acme/widgets", item),
            want
        );
    }
}

#[test]
fn a_project_on_another_instance_is_named_by_its_full_address() {
    // Given: a project on a self-hosted instance, with and without a trailing slash, and a
    // subgroup project on gitlab.com or with no host at all.
    let cases = [
        (
            Some("https://gitlab.mycompany.com"),
            "acme/widgets",
            "glab repo view https://gitlab.mycompany.com/acme/widgets",
        ),
        (
            Some("https://gitlab.mycompany.com/"),
            "acme/widgets",
            "glab repo view https://gitlab.mycompany.com/acme/widgets",
        ),
        (
            Some("https://GitLab.com:443/"),
            "acme/platform/widgets",
            "glab repo view acme/platform/widgets",
        ),
        (
            None,
            "acme/platform/widgets",
            "glab repo view acme/platform/widgets",
        ),
    ];
    for (host, project, want) in cases {
        // When / Then: only another instance than gitlab.com gets its address in front.
        assert_eq!(lines(host, project, Item::Project), [want], "{host:?}");
    }
}

#[test]
fn a_project_without_a_path_has_no_command() {
    // Given / When / Then: no -R can be formed, so nothing is offered.
    assert!(lines(None, "", Item::MergeRequest { iid: 7 }).is_empty());
}

#[test]
fn a_ref_with_shell_characters_is_quoted_so_a_paste_runs_one_command() {
    // Given: refs that a shell reads literally, and refs that carry metacharacters or quotes.
    let cases = [
        ("main", "main"),
        ("release-1.2.3", "release-1.2.3"),
        ("user@team/topic_x+y=z:%,", "user@team/topic_x+y=z:%,"),
        ("fix;rm -rf ~", "'fix;rm -rf ~'"),
        ("release$(date)", "'release$(date)'"),
        ("feat(login)", "'feat(login)'"),
        ("wip&test", "'wip&test'"),
        ("topic#7", "'topic#7'"),
        ("back`tick`", "'back`tick`'"),
        ("it's", r"'it'\''s'"),
        ("two words", "'two words'"),
        ("café", "'café'"),
    ];
    for (git_ref, quoted) in cases {
        // When: the commands of a pipeline on that ref are made.
        let lines = lines(None, "acme/widgets", Item::Pipeline { id: 1, git_ref });

        // Then: the ref view names the ref in its shell-safe form.
        assert_eq!(lines[1], format!("glab ci view {quoted} -R acme/widgets"));
    }
}

#[test]
fn a_project_path_with_shell_characters_is_quoted() {
    // Given / When: a project path that a shell would split.
    let lines = lines(None, "acme/my widgets", Item::Job { id: 5 });

    // Then: the path is one quoted word.
    assert_eq!(lines[0], "glab ci trace 5 -R 'acme/my widgets'");
}
