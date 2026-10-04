package main

import (
	"context"
	"fmt"

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
