// Command flipper runs the Flipper web server: a small self-hosted bridge
// that lets you paste a Spotweb release link, pick a SABnzbd category, and
// send it straight to SABnzbd.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RickDB/Flipper/internal/auth"
	"github.com/RickDB/Flipper/internal/config"
	"github.com/RickDB/Flipper/internal/store"
	buildversion "github.com/RickDB/Flipper/internal/version"
	"github.com/RickDB/Flipper/internal/web"
)

func main() {
	showVersion := flag.Bool("version", false, "print the Flipper version and exit")
	resetAdminUsername := flag.String("reset-admin-username", "", "rename the admin account (use with -reset-admin-password, or alone)")
	resetAdminPassword := flag.String("reset-admin-password", "", "reset the admin account password (minimum 8 characters)")
	flag.Parse()

	if *showVersion {
		fmt.Println(buildversion.Current())
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.Load()

	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		logger.Error("could not open database", "path", cfg.DatabasePath, "error", err)
		os.Exit(1)
	}
	defer st.Close()

	if *resetAdminUsername != "" || *resetAdminPassword != "" {
		if err := resetAdmin(st, *resetAdminUsername, *resetAdminPassword); err != nil {
			fmt.Fprintln(os.Stderr, "admin reset failed:", err)
			os.Exit(1)
		}
		fmt.Println("Flipper admin account updated")
		return
	}

	if err := bootstrapAdmin(st, cfg); err != nil {
		logger.Error("admin bootstrap failed", "error", err)
		os.Exit(1)
	}

	sessions := auth.NewManager(30 * 24 * time.Hour)
	srv, err := web.NewServer(st, sessions, logger, buildversion.Current())
	if err != nil {
		logger.Error("could not start web server", "error", err)
		os.Exit(1)
	}

	httpServer := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		logger.Info("shutting down")
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	logger.Info("Flipper starting", "version", buildversion.Current(), "listen", cfg.ListenAddress, "db", cfg.DatabasePath)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// bootstrapAdmin creates the admin account from FLIPPER_INITIAL_USERNAME /
// FLIPPER_INITIAL_PASSWORD when no admin exists yet — handy for Docker
// Compose deployments that want a non-interactive first boot. When those
// variables are unset, Flipper falls back to the /setup wizard in the UI.
func bootstrapAdmin(st *store.Store, cfg config.Config) error {
	if st.HasAdmin() {
		return nil
	}
	if cfg.InitialAdminUsername == "" || cfg.InitialAdminPassword == "" {
		return nil
	}
	if len(cfg.InitialAdminPassword) < 8 {
		return errors.New("FLIPPER_INITIAL_PASSWORD must be at least 8 characters")
	}
	hash, err := auth.HashPassword(cfg.InitialAdminPassword)
	if err != nil {
		return err
	}
	_, err = st.CreateUser(cfg.InitialAdminUsername, hash, true)
	return err
}

func resetAdmin(st *store.Store, username, password string) error {
	var admin *store.User
	for _, u := range st.ListUsers() {
		if u.IsAdmin {
			uu := u
			admin = &uu
			break
		}
	}
	if admin == nil {
		return errors.New("no admin account exists yet — start Flipper normally and use the /setup page instead")
	}
	if password != "" {
		if len(password) < 8 {
			return errors.New("password must be at least 8 characters")
		}
		hash, err := auth.HashPassword(password)
		if err != nil {
			return err
		}
		if err := st.SetUserPassword(admin.ID, hash); err != nil {
			return err
		}
	}
	if username != "" && username != admin.Username {
		// There's no rename in the store API by design (usernames are the
		// natural key); recreate isn't safe here without extra plumbing, so
		// keep this limited to what the store actually supports today.
		return errors.New("renaming the admin account isn't supported yet; use -reset-admin-password only")
	}
	return nil
}
