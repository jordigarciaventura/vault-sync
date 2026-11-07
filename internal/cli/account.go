package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
	"github.com/jordigarciaventura/vault-sync/internal/config"
)

type accountFlags struct {
	config  string
	backend string
	verbose bool
}

func (f *accountFlags) register(cmd *cobra.Command, types []string) {
	cmd.Flags().StringVarP(&f.backend, "backend", "b", "", "backend to use when no "+config.DefaultFileName+" is found ("+strings.Join(types, ", ")+")")
	cmd.Flags().StringVarP(&f.config, "config", "c", "", "path to the manifest (default: ./"+config.DefaultFileName+", searched upwards; optional)")
	cmd.Flags().BoolVarP(&f.verbose, "verbose", "v", false, "print diagnostic details (never secret values)")
}

func newLoginCommand(a *app) *cobra.Command {
	var flags accountFlags
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to the backend, or show who is logged in",
		Long: `Log in to the backend interactively. If you are already logged in, show the
current account instead.

The backend, expected account and server are taken from secrets.yaml when
one is found; otherwise pass --backend. A session to another server than the
configured one is replaced by a login to that server. pull and push also log in
automatically when needed, so running this first is optional.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, account, err := a.accountBackend(flags)
			if err != nil {
				return err
			}
			defer func() { _ = b.Close() }()
			am, err := accountManager(b)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			st, err := am.Status(cmd.Context())
			if err != nil {
				return err
			}
			// A session to another server than the configured one is
			// replaced by a login to the configured server.
			fresh := !st.LoggedIn || st.ServerMismatch
			if fresh {
				if st, err = am.Login(cmd.Context()); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(out, "\nLogged in to %s.\n\n", b.Name())
			} else {
				_, _ = fmt.Fprintf(out, "Already logged in to %s.\n\n", b.Name())
			}
			printAccount(out, st, account, !fresh)
			return nil
		},
	}
	flags.register(cmd, a.registry.Types())
	return cmd
}

func newLogoutCommand(a *app) *cobra.Command {
	var flags accountFlags
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Log out of the backend",
		Long: `Log out of the backend. The backend is taken from secrets.yaml when one is
found; otherwise pass --backend.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, _, err := a.accountBackend(flags)
			if err != nil {
				return err
			}
			defer func() { _ = b.Close() }()
			am, err := accountManager(b)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			st, err := am.Status(cmd.Context())
			if err != nil {
				return err
			}
			if !st.LoggedIn {
				_, _ = fmt.Fprintf(out, "Not logged in to %s.\n", b.Name())
				return nil
			}
			if err := am.Logout(cmd.Context()); err != nil {
				return err
			}
			if st.User != "" {
				_, _ = fmt.Fprintf(out, "Logged out of %s (%s).\n", b.Name(), st.User)
			} else {
				_, _ = fmt.Fprintf(out, "Logged out of %s.\n", b.Name())
			}
			return nil
		},
	}
	flags.register(cmd, a.registry.Types())
	return cmd
}

// accountBackend builds the backend for login/logout. The manifest is
// optional: without one, --backend (or the only registered backend) is used.
func (a *app) accountBackend(flags accountFlags) (backend.Backend, string, error) {
	logger := newLogger(a.stderr, flags.verbose)
	cfg := backend.Config{Logger: logger}

	path := flags.config
	if path == "" {
		if wd, err := os.Getwd(); err == nil {
			path, _ = config.Find(wd)
		}
	}
	if path != "" {
		m, err := config.Load(path, config.Options{KnownBackends: a.registry.Types()})
		if err != nil {
			return nil, "", err
		}
		logger.Debug("loaded config", "path", m.Path)
		if flags.backend != "" && flags.backend != m.Backend.Type {
			return nil, "", fmt.Errorf("--backend %s conflicts with backend %q in %s; drop the flag", flags.backend, m.Backend.Type, m.Path)
		}
		cfg.Type, cfg.Account, cfg.Environment, cfg.Server = m.Backend.Type, m.Backend.Account, m.Backend.Environment, m.Backend.Server
	} else {
		types := a.registry.Types()
		switch {
		case flags.backend != "":
			cfg.Type = flags.backend
		case len(types) == 1:
			cfg.Type = types[0]
		default:
			return nil, "", fmt.Errorf("no %s found; run this command inside a project, or pass --config or --backend (%s)",
				config.DefaultFileName, strings.Join(types, ", "))
		}
	}

	b, err := a.registry.New(cfg)
	if err != nil {
		return nil, "", err
	}
	return b, cfg.Account, nil
}

func accountManager(b backend.Backend) (backend.AccountManager, error) {
	am, ok := b.(backend.AccountManager)
	if !ok {
		return nil, fmt.Errorf("the %s backend does not support login and logout; authenticate with the provider's own tools", b.Name())
	}
	return am, nil
}

// printAccount shows the login state. The vault state is omitted right after a
// login because the session only lives as long as this process.
func printAccount(out io.Writer, st backend.AccountStatus, expected string, showVault bool) {
	if st.User != "" {
		_, _ = fmt.Fprintf(out, "Account: %s\n", st.User)
	}
	if st.Server != "" {
		_, _ = fmt.Fprintf(out, "Server:  %s\n", st.Server)
	}
	if showVault {
		state := "unlocked"
		if st.Locked {
			state = "locked (pull and push unlock it when needed)"
		}
		_, _ = fmt.Fprintf(out, "Vault:   %s\n", state)
	}
	if st.ServerMismatch {
		_, _ = fmt.Fprintf(out, "\nWarning: %s expects another server; run `vault-sync login` to log in to it.\n", config.DefaultFileName)
	}
	if st.AccountMismatch {
		_, _ = fmt.Fprintf(out, "\nWarning: %s expects account %q; run `vault-sync logout` and `vault-sync login` to switch.\n",
			config.DefaultFileName, expected)
	}
}
