use std::fs;
use std::path::PathBuf;
use std::thread;
use std::time::Duration;

use lazylab::gitlab::Project;
use lazylab::store::Store;
use tempfile::TempDir;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;
use url::Url;

const HOST: &str = "https://gitlab.example.com";
const PROJECTS: &str = "projects_https_gitlab_example_com.json";
const FAVORITES: &str = "favorites_https_gitlab_example_com.json";
/// The `cached_at` of `go_projects.json`.
const GO_CACHED_AT: &str = "2026-10-12T23:31:49.628521-04:00";
const NOW: &str = "2026-10-14T12:00:00Z";

fn at(text: &str) -> OffsetDateTime {
    OffsetDateTime::parse(text, &Rfc3339).unwrap()
}

fn fixture(name: &str) -> Vec<u8> {
    fs::read(PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures/store").join(name))
        .unwrap()
}

fn store_holding(files: &[(&str, &[u8])]) -> (TempDir, Store) {
    let dir = tempfile::tempdir().unwrap();
    for (name, data) in files {
        fs::write(dir.path().join(name), data).unwrap();
    }
    let store = Store::new(dir.path().to_path_buf(), &Url::parse(HOST).unwrap());
    (dir, store)
}

fn projects() -> Vec<Project> {
    let project = |id, name: &str| Project {
        id,
        name: name.into(),
        path_with_namespace: format!("acme/{name}"),
        description: None,
        web_url: format!("{HOST}/acme/{name}"),
        ssh_url_to_repo: format!("git@gitlab.example.com:acme/{name}.git"),
        visibility: "private".into(),
        default_branch: Some("main".into()),
        star_count: 0,
        last_activity_at: Some(at("2026-10-13T03:17:35.257Z")),
    };
    let api = project(9001, "api");
    let web = Project {
        description: Some("The public site".into()),
        visibility: "internal".into(),
        default_branch: None,
        star_count: 1,
        last_activity_at: None,
        ..project(9002, "web")
    };
    vec![api, web]
}

#[test]
fn a_cache_the_go_release_wrote_loads_with_its_recorded_total() {
    // Given: a cache the Go release wrote, holding two of 135 projects.
    let (_dir, store) = store_holding(&[(PROJECTS, &fixture("go_projects.json"))]);

    // When: it loads an hour after it was written.
    let cached = store
        .load_projects(at(GO_CACHED_AT) + Duration::from_secs(3600))
        .unwrap()
        .expect("a cache");

    // Then: the projects come back with the total the Go release recorded.
    assert_eq!(cached.total, 135);
    let [api, web] = &cached.projects[..] else {
        panic!("two projects, got {:?}", cached.projects);
    };
    assert_eq!((api.id, api.name.as_str()), (9001, "api"));
    assert_eq!(api.path_with_namespace, "acme/api");
    assert_eq!(api.description, None);
    assert_eq!(api.web_url, "https://gitlab.example.com/acme/api");
    assert_eq!(api.ssh_url_to_repo, "git@gitlab.example.com:acme/api.git");
    assert_eq!(api.visibility, "private");
    assert_eq!(api.default_branch.as_deref(), Some("main"));
    assert_eq!(api.last_activity_at, Some(at("2026-10-13T03:17:35.257Z")));
    assert_eq!(web.description.as_deref(), Some("The public site"));
    assert_eq!(web.star_count, 1);
    assert_eq!(web.last_activity_at, Some(at("2026-10-13T03:11:29.75Z")));
}

#[test]
fn a_cache_older_than_a_day_is_a_miss() {
    // Given: the cache the Go release wrote.
    let (_dir, store) = store_holding(&[(PROJECTS, &fixture("go_projects.json"))]);
    let day = Duration::from_secs(24 * 3600);

    // When: it loads on the last instant of its day, and one second after.
    let within = store.load_projects(at(GO_CACHED_AT) + day).unwrap();
    let after = store
        .load_projects(at(GO_CACHED_AT) + day + Duration::from_secs(1))
        .unwrap();

    // Then: only the first finds it.
    assert!(within.is_some());
    assert!(after.is_none());
}

#[test]
fn a_missing_empty_or_other_version_cache_is_a_miss() {
    let empty = br#"{"version":2,"host":"https://gitlab.example.com","cached_at":"2026-10-14T11:00:00Z","total_projects":0,"projects":[]}"#;
    let version_one = br#"{"version":1,"host":"https://gitlab.example.com","cached_at":"2026-10-14T11:00:00Z","projects":[{"ID":1,"Name":"api"}]}"#;
    let cases: [(&str, &[(&str, &[u8])]); 3] = [
        ("missing", &[]),
        ("empty", &[(PROJECTS, empty)]),
        ("version 1", &[(PROJECTS, version_one)]),
    ];
    for (case, files) in cases {
        // Given: the cache folder for the case.
        let (_dir, store) = store_holding(files);

        // When: the cache loads.
        let cached = store.load_projects(at(NOW)).unwrap();

        // Then: nothing comes back, and nothing fails.
        assert!(cached.is_none(), "{case}");
    }
}

#[test]
fn a_saved_cache_is_the_file_the_go_release_decodes() {
    // Given: an empty cache folder.
    let (dir, store) = store_holding(&[]);

    // When: two of 135 projects are saved.
    store.save_projects(&projects(), 135, at(NOW)).unwrap();

    // Then: the file is the one the Go test decodes.
    let written = fs::read(dir.path().join(PROJECTS)).unwrap();
    assert_eq!(
        String::from_utf8(written).unwrap(),
        String::from_utf8(fixture("rust_projects.json")).unwrap()
    );
}

#[test]
fn a_saved_cache_loads_back_as_the_same_projects() {
    // Given: two projects saved with a total that is too small.
    let (_dir, store) = store_holding(&[]);
    store.save_projects(&projects(), 0, at(NOW)).unwrap();

    // When: the cache loads.
    let cached = store.load_projects(at(NOW)).unwrap().expect("a cache");

    // Then: the projects come back as they were, and the total counts at least them.
    assert_eq!(cached.projects, projects());
    assert_eq!(cached.total, 2);
}

#[test]
fn saving_no_projects_keeps_the_cache_there_is() {
    // Given: the cache the Go release wrote.
    let (dir, store) = store_holding(&[(PROJECTS, &fixture("go_projects.json"))]);

    // When: an empty list is saved.
    store.save_projects(&[], 0, at(NOW)).unwrap();

    // Then: the file is unchanged.
    assert_eq!(
        fs::read(dir.path().join(PROJECTS)).unwrap(),
        fixture("go_projects.json")
    );
}

#[test]
fn favorites_load_in_the_order_they_were_saved_in() {
    // Given: favorites in the format both releases write.
    let (_dir, store) = store_holding(&[(FAVORITES, &fixture("favorites.json"))]);

    // When: they load.
    let favorites = store.load_favorites().unwrap();

    // Then: they keep the order of the file.
    assert_eq!(favorites, [9002, 9001]);
}

#[test]
fn missing_null_or_other_version_favorites_load_as_none() {
    let null = br#"{"version":2,"host":"https://gitlab.example.com","project_ids":null}"#;
    let version_one = br#"{"version":1,"host":"https://gitlab.example.com","project_ids":[3]}"#;
    let cases: [(&str, &[(&str, &[u8])]); 3] = [
        ("missing", &[]),
        ("null", &[(FAVORITES, null)]),
        ("version 1", &[(FAVORITES, version_one)]),
    ];
    for (case, files) in cases {
        // Given: the cache folder for the case.
        let (_dir, store) = store_holding(files);

        // When: the favorites load.
        let favorites = store.load_favorites().unwrap();

        // Then: there are none, and nothing fails.
        assert!(favorites.is_empty(), "{case}");
    }
}

#[test]
fn saved_favorites_are_the_bytes_the_go_release_writes() {
    // Given: an empty cache folder.
    let (dir, store) = store_holding(&[]);

    // When: two favorites are saved.
    store.save_favorites(&[9002, 9001]).unwrap();

    // Then: the file is byte for byte what the Go release writes for them.
    assert_eq!(
        String::from_utf8(fs::read(dir.path().join(FAVORITES)).unwrap()).unwrap(),
        String::from_utf8(fixture("favorites.json")).unwrap()
    );
}

#[test]
fn two_writers_at_once_leave_favorites_a_reader_can_load() {
    // Given: two writers that save different favorites, over and over.
    let (dir, store) = store_holding(&[]);
    let writer = |ids: &'static [u64]| {
        let store = store.clone();
        thread::spawn(move || {
            for _ in 0..200 {
                store.save_favorites(ids).unwrap();
            }
        })
    };
    let writers = [writer(&[1, 2, 3]), writer(&[4, 5])];

    // When: a reader loads while they write.
    let mut loads = 0;
    while writers.iter().any(|w| !w.is_finished()) {
        let favorites = store.load_favorites().expect("a whole file");
        // Then: every load is one writer's list, or nothing before the first write lands.
        assert!(
            [&[][..], &[1, 2, 3], &[4, 5]].contains(&&favorites[..]),
            "{favorites:?}"
        );
        loads += 1;
    }
    for writer in writers {
        writer.join().unwrap();
    }
    assert!(loads > 0);

    // And: no temporary file is left behind.
    let names: Vec<_> = fs::read_dir(dir.path())
        .unwrap()
        .map(|entry| entry.unwrap().file_name())
        .collect();
    assert_eq!(names, [FAVORITES]);
}

#[test]
fn the_files_are_named_for_the_scheme_host_and_port() {
    // Given: a host on plain HTTP, with a port and a path.
    let dir = tempfile::tempdir().unwrap();
    let host = Url::parse("http://gitlab.example:8080/gitlab").unwrap();
    let store = Store::new(dir.path().to_path_buf(), &host);

    // When: favorites and projects are saved.
    store.save_favorites(&[1]).unwrap();
    store.save_projects(&projects(), 2, at(NOW)).unwrap();

    // Then: the names are the ones the Go release gives that host.
    let mut names: Vec<_> = fs::read_dir(dir.path())
        .unwrap()
        .map(|entry| entry.unwrap().file_name().into_string().unwrap())
        .collect();
    names.sort();
    assert_eq!(
        names,
        [
            "favorites_http_gitlab_example-8080.json",
            "projects_http_gitlab_example-8080.json"
        ]
    );
}

#[test]
fn saving_creates_the_cache_folder() {
    // Given: a cache folder that does not exist yet.
    let dir = tempfile::tempdir().unwrap();
    let folder = dir.path().join("lazylab");
    let store = Store::new(folder.clone(), &Url::parse(HOST).unwrap());

    // When: favorites are saved.
    store.save_favorites(&[1]).unwrap();

    // Then: the folder exists and holds them.
    assert!(folder.join(FAVORITES).is_file());
}
