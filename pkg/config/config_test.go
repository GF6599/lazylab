package config

import "testing"

func TestLoadRequiresToken(t *testing.T) {
	v := NewViper()
	v.Set(KeyToken, "")

	if _, err := Load(v); err == nil {
		t.Fatalf("expected error when token missing")
	}
}

func TestLoadDefaults(t *testing.T) {
	v := NewViper()
	v.Set(KeyToken, "token")

	cfg, err := Load(v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Host != ensureTrailingSlash(DefaultHost) {
		t.Fatalf("expected host %q, got %q", ensureTrailingSlash(DefaultHost), cfg.Host)
	}
	if cfg.ProjectsPerPage != DefaultProjectPage {
		t.Fatalf("expected default per page %d, got %d", DefaultProjectPage, cfg.ProjectsPerPage)
	}
}

func TestLoadCustomHostAndPaging(t *testing.T) {
	v := NewViper()
	v.Set(KeyToken, "token")
	v.Set(KeyHost, "gitlab.internal")
	v.Set(KeyProjectsPerPage, 10)

	cfg, err := Load(v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantHost := "https://gitlab.internal/"
	if cfg.Host != wantHost {
		t.Fatalf("expected host %q, got %q", wantHost, cfg.Host)
	}
	if cfg.ProjectsPerPage != 10 {
		t.Fatalf("expected per page 10, got %d", cfg.ProjectsPerPage)
	}
}
