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

// pinOpenCodeDB makes an OpenCode v2 server ocman launches write the
// database ocman reads: a non-default -db is exported as OPENCODE_DB
// (v2's override), which the launcher forwards. An explicit OPENCODE_DB
// always wins. v1 ignores the variable.
func pinOpenCodeDB(path string) {
	if os.Getenv("OPENCODE_DB") != "" || path == "" || path == db.DefaultDBPath() {
		return
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	_ = os.Setenv("OPENCODE_DB", path)
}
