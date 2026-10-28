package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
	"github.com/ocman/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

const v2PasswordSetting = "opencode.v2_server_password"

// startupOpenCodePassword keeps explicit credentials authoritative. A shared
// service rejecting them must fail closed rather than launch another writer.
func startupOpenCodePassword(ctx context.Context, st *state.DB, configured string, enabled, useService bool) (string, error) {
	if !enabled || configured != "" || !ocv2.InstalledV2() {
		return configured, nil
	}
	if useService {
		return sharedV2Password(ctx, st)
	}
	return v2ServerPassword(ctx, st)
}

// v2ServerPassword returns the password ocman's OpenCode v2 server runs
// with. v2 always requires one (it invents a random password otherwise,
// which ocman could not read back), so without a configured password
// ocman generates one and keeps it in state.db: a restarted ocman can
// then keep using the server it launched before.
func v2ServerPassword(ctx context.Context, st *state.DB) (string, error) {
	if pw, ok, err := st.GetSecretSetting(ctx, v2PasswordSetting); err != nil || ok {
		return pw, err
	}
	pw, err := resolveOpenCodePassword("", true)
	if err != nil {
		return "", err
	}
	if err := st.SetSecretSetting(ctx, v2PasswordSetting, pw); err != nil {
		return "", fmt.Errorf("storing OpenCode v2 server password: %w", err)
	}
	return pw, nil
}

// sharesServiceDB reports whether OpenCode's background service writes
// the database ocman reads. The service runs on the default database, so
// with -db or OPENCODE_DB pointing elsewhere (development isolation)
// ocman must keep its own server.
func sharesServiceDB(path string) bool {
	if !ocv2.InstalledV2() || path == "" {
		return false
	}
	return sameFile(path, db.DefaultDBPath())
}

func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// sharedV2Password picks the password for a machine that uses OpenCode's
// background service: the service's own, so ocman can call it and a
// server ocman launches accepts the same clients. Without one, ocman's
// stored password is used. A service rejecting it returns an authentication
// error without launching a competing server.
func sharedV2Password(ctx context.Context, st *state.DB) (string, error) {
	pw, err := ocv2.ServicePassword()
	if err != nil {
		log.WithError(err).Warn("ignoring OpenCode service password; using ocman's own")
	}
	if pw != "" {
		return pw, nil
	}
	return v2ServerPassword(ctx, st)
}

// pinOpenCodeDB makes ocman read the database the OpenCode v2 server it
// launches writes. An explicit -db wins and is exported as OPENCODE_DB
// (v2's override, forwarded by the launcher). Otherwise an OPENCODE_DB
// the user set is read too, instead of the default path. It returns the
// database path ocman opens. v1 ignores OPENCODE_DB.
func pinOpenCodeDB(path string, explicit bool) string {
	if !ocv2.InstalledV2() {
		return path // v1's reader remains selected solely by -db
	}
	if env := os.Getenv("OPENCODE_DB"); env != "" && !explicit {
		path = env
	}
	if path == "" {
		path = db.DefaultDBPath()
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	_ = os.Setenv("OPENCODE_DB", path)
	return path
}
