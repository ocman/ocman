package remote

import (
	"context"

	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/state"
)

func (c *RemoteConn) RegisterWebhookInbox(ctx context.Context, routineID, relayURL, enrollmentToken string) (state.WebhookInbox, error) {
	return c.RegisterWebhookInboxWithSecret(ctx, routineID, relayURL, enrollmentToken, "", "")
}

func (c *RemoteConn) RegisterWebhookInboxWithSecret(ctx context.Context, routineID, relayURL, enrollmentToken, secret, secretHeader string) (state.WebhookInbox, error) {
	client := c.Client()
	if client == nil {
		return state.WebhookInbox{}, ErrRemoteOffline
	}
	payload, err := marshalJSON(struct {
		RoutineID       string `json:"routineId"`
		RelayURL        string `json:"relayUrl"`
		EnrollmentToken string `json:"enrollmentToken"`
		Secret          string `json:"secret"`
		SecretHeader    string `json:"secretHeader"`
	}{routineID, relayURL, enrollmentToken, secret, secretHeader})
	if err != nil {
		return state.WebhookInbox{}, err
	}
	resp, err := client.RegisterWebhookInbox(ctx, &pb.JsonReq{Payload: payload})
	if err != nil {
		return state.WebhookInbox{}, err
	}
	var inbox state.WebhookInbox
	return inbox, unmarshalJSON(resp.Payload, &inbox)
}

func (c *RemoteConn) PollWebhookInbox(ctx context.Context, routineID string) error {
	client := c.Client()
	if client == nil {
		return ErrRemoteOffline
	}
	payload, err := marshalJSON(struct {
		RoutineID string `json:"routineId"`
	}{routineID})
	if err != nil {
		return err
	}
	_, err = client.PollWebhookInbox(ctx, &pb.JsonReq{Payload: payload})
	return err
}
