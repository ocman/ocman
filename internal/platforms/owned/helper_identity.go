package owned

import "context"

func (a *Adapter) HelperSessionIdentity(ctx context.Context, native string) (string, error) {
	row, err := a.canonical(ctx, native)
	return row.ID, err
}
