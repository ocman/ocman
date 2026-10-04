package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/NoUseFreak/ocman/internal/db"

	"github.com/NoUseFreak/ocman/internal/state"
)

const v2PasswordSetting = "opencode.v2_server_password"

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

// pinOpenCodeDB makes ocman read the database the OpenCode v2 server it
// launches writes. An explicit -db wins and is exported as OPENCODE_DB
// (v2's override, forwarded by the launcher). Otherwise an OPENCODE_DB
// the user set is read too, instead of the default path. It returns the
// database path ocman opens. v1 ignores OPENCODE_DB.
func pinOpenCodeDB(path string, explicit bool) string {
	if env := os.Getenv("OPENCODE_DB"); env != "" && !explicit {
		return env
	}
	if path == "" || path == db.DefaultDBPath() {
		return path
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	_ = os.Setenv("OPENCODE_DB", path)
	return path
}
