// This file is part of gofaxserver - https://github.com/sagostin/gofaxserver
// Copyright (C) 2025-2026 Shaun Agostinho
//
// This program is free software; you can redistribute it and/or
// modify it under the terms of the GNU General Public License
// as published by the Free Software Foundation; version 2
// of the License.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program; if not, write to the Free Software
// Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301, USA.

package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"gofaxportal/internal/api"
	"gofaxportal/internal/auth"
	"gofaxportal/internal/config"
	"gofaxportal/internal/crypto"
	"gofaxportal/internal/db"
	"gofaxportal/internal/fsclient"
	"gofaxportal/internal/models"
	"gofaxportal/internal/poller"

	"github.com/kataras/iris/v12"
	"gorm.io/gorm"
)

func main() {
	cfgPath := flag.String("c", "", "path to portal config.json (env vars may fully configure instead)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config: %v", err)
	}

	gdb, err := db.Connect(cfg)
	if err != nil {
		log.Fatalf("%v", err)
	}

	box, err := crypto.NewBox(cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("encryption key: %v", err)
	}

	fx := fsclient.New(cfg.GofaxServer.BaseURL, cfg.GofaxServer.AdminAPIKey)

	if err := bootstrapAdmin(gdb, cfg); err != nil {
		log.Fatalf("bootstrap admin: %v", err)
	}

	authSvc := auth.NewService(gdb)
	stop := make(chan struct{})
	authSvc.StartJanitor(stop)

	srv := api.New(cfg, gdb, authSvc, box, fx)
	app := srv.BuildApp()

	pollStop := make(chan struct{})
	go poller.New(cfg, gdb, fx, box).Run(pollStop)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		close(stop)
		close(pollStop)
		_ = app.Shutdown(ctx)
	}()

	log.Printf("[portal] listening on %s (gofaxserver at %s)", cfg.Listen, cfg.GofaxServer.BaseURL)
	if err := app.Run(iris.Addr(cfg.Listen), iris.WithoutServerError(iris.ErrServerClosed)); err != nil {
		log.Fatalf("http: %v", err)
	}
}

// bootstrapAdmin creates the first admin account when the portal DB has none.
func bootstrapAdmin(gdb *gorm.DB, cfg *config.Config) error {
	var count int64
	if err := gdb.Model(&models.PortalUser{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if cfg.BootstrapAdmin.Password == "" || cfg.BootstrapAdmin.Username == "" {
		log.Print("WARNING: no users exist and bootstrap_admin.password is empty — set it in config or PORTAL_BOOTSTRAP_PASSWORD to create the first admin")
		return nil
	}
	hash, err := auth.HashPassword(cfg.BootstrapAdmin.Password)
	if err != nil {
		return err
	}
	u := &models.PortalUser{
		Username:     cfg.BootstrapAdmin.Username,
		Email:        cfg.BootstrapAdmin.Email,
		PasswordHash: hash,
		Role:         models.RoleAdmin,
		Active:       true,
	}
	if err := gdb.Create(u).Error; err != nil {
		return err
	}
	log.Printf("[portal] bootstrapped initial admin account %q — change its password after first login", u.Username)
	return nil
}
