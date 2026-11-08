package hostsvc

import "context"

func (stubHost) CreateOneOffDirectory(context.Context) (string, error) {
	return "/tmp/one-off", nil
}
