package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"gitlab-tui-codex/internal/gitlab"
	"gitlab-tui-codex/internal/ui"
	"gitlab-tui-codex/pkg/config"
	"gitlab-tui-codex/pkg/logging"
)

const keyLogLevel = "log_level"

var (
	cfgFile  string
	settings = config.NewViper()
	rootCmd  = &cobra.Command{
		Use:   "gitlab-tui",
		Short: "Terminal UI for browsing GitLab projects, pipelines, and files",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := readConfigFile(); err != nil {
				return err
			}

			if err := logging.SetLevel(settings.GetString(keyLogLevel)); err != nil {
				return err
			}
			log := logging.Logger()
			log.Info("starting gitlab-tui", "config_file", cfgFile)

			cfg, err := config.Load(settings)
			if err != nil {
				log.Error("configuration error", "err", err)
				return err
			}
			log.Info("configuration loaded", "host", cfg.Host, "projects_per_page", cfg.ProjectsPerPage)

			client, err := gitlab.NewClient(cfg)
			if err != nil {
				log.Error("unable to create gitlab client", "err", err)
				return err
			}
			log.Debug("gitlab client created")

			program := tea.NewProgram(ui.NewModel(client), tea.WithAltScreen())
			if err := program.Start(); err != nil {
				log.Error("tui terminated with error", "err", err)
				return err
			}

			log.Info("gitlab-tui exited successfully")
			return nil
		},
	}
)

// Execute runs the cobra command tree.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Path to optional config file")
	rootCmd.PersistentFlags().String("host", config.DefaultHost, "GitLab base URL")
	rootCmd.PersistentFlags().String("token", "", "GitLab personal access token (or set GITLAB_TOKEN)")
	rootCmd.PersistentFlags().Int("projects-per-page", config.DefaultProjectPage, "Number of membership projects to load")
	rootCmd.PersistentFlags().String("log-level", "info", "Log verbosity (debug, info, warn, error)")

	mustBind(config.KeyHost, "host")
	mustBind(config.KeyToken, "token")
	mustBind(config.KeyProjectsPerPage, "projects-per-page")
	if err := settings.BindPFlag(keyLogLevel, rootCmd.PersistentFlags().Lookup("log-level")); err != nil {
		panic(fmt.Sprintf("bind flag log-level: %v", err))
	}
	settings.SetDefault(keyLogLevel, "info")
}

func mustBind(key, flag string) {
	if err := settings.BindPFlag(key, rootCmd.PersistentFlags().Lookup(flag)); err != nil {
		panic(fmt.Sprintf("bind flag %s: %v", flag, err))
	}
}

func readConfigFile() error {
	if cfgFile == "" {
		return nil
	}
	settings.SetConfigFile(cfgFile)
	logging.Logger().Info("loading config file", "path", cfgFile)
	if err := settings.ReadInConfig(); err != nil {
		logging.Logger().Error("failed to load config file", "path", cfgFile, "err", err)
		return err
	}
	return nil
}
